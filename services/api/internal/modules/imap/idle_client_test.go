package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNetworkWatchSessionReportsIdleChangesAndHeartbeat(t *testing.T) {
	for _, test := range []struct {
		name       string
		changeLine string
		want       WatchReason
	}{
		{name: "change", changeLine: "* 3 EXISTS", want: WatchChanged},
		{name: "heartbeat", want: WatchHeartbeat},
	} {
		t.Run(test.name, func(t *testing.T) {
			serverTLS, roots := probeCertificate(t)
			address := serveIdleWatchIMAP(t, serverTLS, true, test.changeLine, false)
			factory, credentials := watchFactoryFixture(t, address, roots, 100*time.Millisecond)
			session, err := factory.Open(context.Background(), credentials)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			got, err := session.Wait(context.Background(), 20*time.Millisecond)
			if err != nil || got != test.want {
				t.Fatalf("wait = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestNetworkWatchFactoryFallsBackWithoutIdle(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	address := serveIdleWatchIMAP(t, serverTLS, false, "", false)
	factory, credentials := watchFactoryFixture(t, address, roots, 100*time.Millisecond)
	session, err := factory.Open(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if session.IdleSupported() {
		t.Fatal("server without IDLE created an IDLE session")
	}
	started := time.Now()
	reason, err := session.Wait(context.Background(), 10*time.Millisecond)
	if err != nil || reason != WatchPoll || time.Since(started) < 8*time.Millisecond {
		t.Fatalf("fallback wait = %q, %v after %s", reason, err, time.Since(started))
	}
}

func TestNetworkWatchSessionReportsDisconnectAndStopsPromptly(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	address := serveIdleWatchIMAP(t, serverTLS, true, "", true)
	factory, credentials := watchFactoryFixture(t, address, roots, time.Second)
	session, err := factory.Open(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Wait(context.Background(), time.Second); !errors.Is(err, ErrWatchDisconnected) {
		t.Fatalf("disconnect error = %v", err)
	}

	address = serveIdleWatchIMAP(t, serverTLS, true, "", false)
	factory, credentials = watchFactoryFixture(t, address, roots, time.Second)
	session, err = factory.Open(context.Background(), credentials)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, waitErr := session.Wait(ctx, time.Minute); done <- waitErr }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled IDLE wait did not stop")
	}
	_ = session.Close()
}

func watchFactoryFixture(t *testing.T, address string, roots *x509.CertPool, timeout time.Duration) (*NetworkWatchFactory, storedCredentials) {
	t.Helper()
	host, port, _ := net.SplitHostPort(address)
	prober, err := NewNetworkProber(timeout, roots)
	if err != nil {
		t.Fatal(err)
	}
	factory, err := NewNetworkWatchFactory(prober)
	if err != nil {
		t.Fatal(err)
	}
	return factory, storedCredentials{
		Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: hostForTLS(host), Port: integerPort(port), TLSMode: TLSImplicit},
	}
}

func serveIdleWatchIMAP(t *testing.T, config *tls.Config, idle bool, changeLine string, disconnect bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		tlsConnection := tls.Server(connection, config)
		if tlsConnection.Handshake() != nil {
			return
		}
		reader, writer := bufio.NewReader(tlsConnection), bufio.NewWriter(tlsConnection)
		writeFolderFixtureLines(writer, "* OK fixture ready")
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return
			}
			tag, command := fields[0], strings.ToUpper(fields[1])
			switch command {
			case "LOGIN":
				writeFolderFixtureLines(writer, tag+" OK authenticated")
			case "CAPABILITY":
				capabilities := "* CAPABILITY IMAP4rev1"
				if idle {
					capabilities += " IDLE"
				}
				writeFolderFixtureLines(writer, capabilities, tag+" OK capability")
			case "SELECT":
				writeFolderFixtureLines(writer, "* 2 EXISTS", tag+" OK selected")
			case "IDLE":
				writeFolderFixtureLines(writer, "+ idling")
				if disconnect {
					return
				}
				if changeLine != "" {
					writeFolderFixtureLines(writer, changeLine)
				}
				if done, readErr := reader.ReadString('\n'); readErr != nil || strings.TrimSpace(done) != "DONE" {
					return
				}
				writeFolderFixtureLines(writer, tag+" OK idle complete")
			case "LOGOUT":
				writeFolderFixtureLines(writer, "* BYE closing", tag+" OK logout")
				return
			default:
				writeFolderFixtureLines(writer, tag+" BAD unsupported")
			}
		}
	}()
	return listener.Addr().String()
}
