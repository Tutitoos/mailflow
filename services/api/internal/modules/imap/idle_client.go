package imap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

type NetworkWatchFactory struct{ prober *NetworkProber }

func NewNetworkWatchFactory(prober *NetworkProber) (*NetworkWatchFactory, error) {
	if prober == nil {
		return nil, ErrWatchConfiguration
	}
	return &NetworkWatchFactory{prober: prober}, nil
}

func (factory *NetworkWatchFactory) Open(ctx context.Context, credentials storedCredentials) (WatchSession, error) {
	connection, reader, writer, err := factory.prober.openAuthenticatedIMAP(ctx, credentials)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (WatchSession, error) {
		_ = connection.Close()
		return nil, err
	}
	if err := connection.SetDeadline(time.Now().Add(factory.prober.timeout)); err != nil {
		return fail(err)
	}
	if err := writeIMAPCommand(writer, "W001 CAPABILITY"); err != nil {
		return fail(classifyConnectionError(ctx, err))
	}
	lines, err := readIMAPResponse(reader, "W001", false)
	if err != nil {
		return fail(classifyConnectionError(ctx, err))
	}
	idle := false
	for _, line := range lines {
		if strings.HasPrefix(strings.ToUpper(line), "* CAPABILITY ") {
			for _, capability := range strings.Fields(strings.TrimSpace(line[len("* CAPABILITY "):])) {
				idle = idle || strings.EqualFold(capability, "IDLE")
			}
		}
	}
	if !idle {
		_ = writeIMAPCommand(writer, "W999 LOGOUT")
		_ = connection.Close()
		return pollWatchSession{}, nil
	}
	if err := writeIMAPCommand(writer, `W002 SELECT "INBOX"`); err != nil {
		return fail(classifyConnectionError(ctx, err))
	}
	if _, err := readIMAPResponse(reader, "W002", false); err != nil {
		return fail(classifyConnectionError(ctx, err))
	}
	if err := connection.SetDeadline(time.Time{}); err != nil {
		return fail(err)
	}
	return &networkWatchSession{connection: connection, reader: reader, writer: writer, operationTimeout: factory.prober.timeout, nextTag: 3}, nil
}

type pollWatchSession struct{}

func (pollWatchSession) IdleSupported() bool { return false }
func (pollWatchSession) Close() error        { return nil }
func (pollWatchSession) Wait(ctx context.Context, duration time.Duration) (WatchReason, error) {
	if !waitContext(ctx, duration) {
		return "", ctx.Err()
	}
	return WatchPoll, nil
}

type networkWatchSession struct {
	connection       net.Conn
	reader           *bufio.Reader
	writer           *bufio.Writer
	operationTimeout time.Duration
	nextTag          int
	closeOnce        sync.Once
}

func (*networkWatchSession) IdleSupported() bool { return true }

func (session *networkWatchSession) Wait(ctx context.Context, duration time.Duration) (WatchReason, error) {
	if duration <= 0 {
		return "", ErrWatchConfiguration
	}
	tag := fmt.Sprintf("W%03d", session.nextTag)
	session.nextTag++
	if err := session.connection.SetDeadline(time.Time{}); err != nil {
		return "", err
	}
	if err := writeIMAPCommand(session.writer, tag+" IDLE"); err != nil {
		return "", classifyConnectionError(ctx, err)
	}
	if err := session.connection.SetReadDeadline(time.Now().Add(duration)); err != nil {
		return "", err
	}
	stopCancellation := context.AfterFunc(ctx, func() { _ = session.connection.SetReadDeadline(time.Now()) })
	defer stopCancellation()
	continuation, err := readBoundedLine(session.reader)
	if err != nil {
		return "", session.waitError(ctx, err)
	}
	if !strings.HasPrefix(continuation, "+") {
		return "", ErrCapability
	}
	for range 4096 {
		line, err := readBoundedLine(session.reader)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var networkError net.Error
			if errors.As(err, &networkError) && networkError.Timeout() {
				if finishErr := session.finishIdle(ctx, tag); finishErr != nil {
					return "", finishErr
				}
				return WatchHeartbeat, nil
			}
			return "", ErrWatchDisconnected
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "* BYE") {
			return "", ErrWatchDisconnected
		}
		if idleChangeResponse(upper) {
			if err := session.finishIdle(ctx, tag); err != nil {
				return "", err
			}
			return WatchChanged, nil
		}
	}
	return "", ErrProtocol
}

func (session *networkWatchSession) finishIdle(ctx context.Context, tag string) error {
	if err := session.connection.SetDeadline(time.Now().Add(session.operationTimeout)); err != nil {
		return err
	}
	if _, err := session.writer.WriteString("DONE\r\n"); err != nil {
		return classifyConnectionError(ctx, err)
	}
	if err := session.writer.Flush(); err != nil {
		return classifyConnectionError(ctx, err)
	}
	if _, err := readIMAPResponse(session.reader, tag, false); err != nil {
		return classifyConnectionError(ctx, err)
	}
	return session.connection.SetDeadline(time.Time{})
}

func (session *networkWatchSession) waitError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrTimeout
	}
	return ErrWatchDisconnected
}

func (session *networkWatchSession) Close() error {
	var closeErr error
	session.closeOnce.Do(func() { closeErr = session.connection.Close() })
	return closeErr
}

func idleChangeResponse(upper string) bool {
	if !strings.HasPrefix(upper, "* ") {
		return false
	}
	fields := strings.Fields(upper)
	if len(fields) < 3 {
		return false
	}
	return fields[2] == "EXISTS" || fields[2] == "EXPUNGE" || fields[2] == "FETCH" || fields[2] == "RECENT"
}

var _ WatchSessionFactory = (*NetworkWatchFactory)(nil)
