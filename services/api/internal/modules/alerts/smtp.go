package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type SMTPConfig struct {
	Host                         string
	Port                         int
	Username, Password, From, To string
	ImplicitTLS                  bool
}
type SMTPSender struct {
	config  SMTPConfig
	timeout time.Duration
}

func NewSMTPSender(config SMTPConfig) (*SMTPSender, error) {
	if config.Host == "" || config.Port < 1 || config.Port > 65535 || config.From == "" || config.To == "" || strings.ContainsAny(config.From+config.To, "\r\n") {
		return nil, ErrInvalid
	}
	return &SMTPSender{config: config, timeout: 15 * time.Second}, nil
}

func (sender *SMTPSender) Send(ctx context.Context, message Message) error {
	address := net.JoinHostPort(sender.config.Host, strconv.Itoa(sender.config.Port))
	dialer := net.Dialer{Timeout: sender.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	if err = connection.SetDeadline(time.Now().Add(sender.timeout)); err != nil {
		_ = connection.Close()
		return err
	}
	if sender.config.ImplicitTLS {
		connection = tls.Client(connection, &tls.Config{ServerName: sender.config.Host, MinVersion: tls.VersionTLS12})
	}
	client, err := smtp.NewClient(connection, sender.config.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !sender.config.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP STARTTLS required")
		}
		if err = client.StartTLS(&tls.Config{ServerName: sender.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if sender.config.Username != "" {
		if err = client.Auth(smtp.PlainAuth("", sender.config.Username, sender.config.Password, sender.config.Host)); err != nil {
			return err
		}
	}
	if err = client.Mail(sender.config.From); err != nil {
		return err
	}
	if err = client.Rcpt(sender.config.To); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	subject := "Mailflow incident"
	if message.Kind == "recovery" {
		subject = "Mailflow recovery"
	}
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s [%s]\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nPolicy: %s\r\nSource: %s\r\nCode: %s\r\nIncident: %s\r\n\r\nOpen Mailflow Admin for remediation guidance.\r\n", sender.config.From, sender.config.To, subject, message.Code, message.Policy, message.Source, message.Code, message.IncidentID)
	if _, err = writer.Write([]byte(body)); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}
