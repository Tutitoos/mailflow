package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

const defaultProbeTimeout = 15 * time.Second

type NetworkProber struct {
	timeout time.Duration
	roots   *x509.CertPool
}

func NewNetworkProber(timeout time.Duration, roots *x509.CertPool) (*NetworkProber, error) {
	if timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalidConfiguration
	}
	return &NetworkProber{timeout: timeout, roots: roots}, nil
}

func DefaultNetworkProber() *NetworkProber {
	prober, _ := NewNetworkProber(defaultProbeTimeout, nil)
	return prober
}

func (prober *NetworkProber) Probe(ctx context.Context, input ConnectInput) (map[string]bool, error) {
	if !input.valid() {
		return nil, ErrInvalidConfiguration
	}
	imapCapabilities, err := prober.probeIMAP(ctx, input)
	if err != nil {
		return nil, err
	}
	smtpCapabilities, err := prober.probeSMTP(ctx, input)
	if err != nil {
		return nil, err
	}
	for key, value := range smtpCapabilities {
		imapCapabilities[key] = value
	}
	return imapCapabilities, nil
}

func (prober *NetworkProber) probeIMAP(ctx context.Context, input ConnectInput) (map[string]bool, error) {
	connection, err := prober.dial(ctx, input.IMAP)
	if err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	defer connection.Close()
	reader := bufio.NewReaderSize(connection, 64<<10)
	writer := bufio.NewWriterSize(connection, 8<<10)
	if input.IMAP.TLSMode == TLSStartTLS {
		if err := expectIMAPGreeting(reader); err != nil {
			return nil, err
		}
		if err := writeIMAPCommand(writer, "A001 STARTTLS"); err != nil {
			return nil, classifyConnectionError(ctx, err)
		}
		if _, err := readIMAPResponse(reader, "A001", false); err != nil {
			if errors.Is(err, ErrProtocol) {
				return nil, ErrCapability
			}
			return nil, classifyConnectionError(ctx, err)
		}
		tlsConnection := tls.Client(connection, prober.tlsConfig(input.IMAP.Host))
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return nil, classifyConnectionError(ctx, err)
		}
		connection = tlsConnection
		reader = bufio.NewReaderSize(connection, 64<<10)
		writer = bufio.NewWriterSize(connection, 8<<10)
	} else if err := expectIMAPGreeting(reader); err != nil {
		return nil, err
	}
	if err := writeIMAPCommand(writer, "A002 CAPABILITY"); err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	lines, err := readIMAPResponse(reader, "A002", false)
	if err != nil {
		if errors.Is(err, ErrProtocol) {
			return nil, ErrCapability
		}
		return nil, classifyConnectionError(ctx, err)
	}
	capabilities := imapCapabilities(lines, input.IMAP.TLSMode)
	login := "A003 LOGIN " + quoteIMAP(input.Username) + " " + quoteIMAP(input.Password)
	if err := writeIMAPCommand(writer, login); err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	if _, err := readIMAPResponse(reader, "A003", true); err != nil {
		return nil, err
	}
	_ = writeIMAPCommand(writer, "A004 LOGOUT")
	return capabilities, nil
}

func (prober *NetworkProber) probeSMTP(ctx context.Context, input ConnectInput) (map[string]bool, error) {
	connection, err := prober.dial(ctx, input.SMTP)
	if err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	defer connection.Close()
	client, err := smtp.NewClient(connection, input.SMTP.Host)
	if err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	defer client.Close()
	if input.SMTP.TLSMode == TLSStartTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return nil, ErrCapability
		}
		if err := client.StartTLS(prober.tlsConfig(input.SMTP.Host)); err != nil {
			return nil, classifyConnectionError(ctx, err)
		}
	}
	advertised, mechanisms := client.Extension("AUTH")
	if !advertised {
		return nil, ErrCapability
	}
	authenticator, err := smtpAuthenticator(mechanisms, input.Username, input.Password, input.SMTP.Host)
	if err != nil {
		return nil, err
	}
	if err := client.Auth(authenticator); err != nil {
		var protocolError *textproto.Error
		if errors.As(err, &protocolError) {
			return nil, ErrAuthentication
		}
		return nil, classifyConnectionError(ctx, err)
	}
	capabilities := map[string]bool{"smtp.tls": true, "smtp.starttls": input.SMTP.TLSMode == TLSStartTLS}
	for extension, key := range map[string]string{"8BITMIME": "smtp.8bitmime", "SMTPUTF8": "smtp.smtputf8", "DSN": "smtp.dsn", "SIZE": "smtp.size"} {
		if supported, _ := client.Extension(extension); supported {
			capabilities[key] = true
		}
	}
	_ = client.Quit()
	return capabilities, nil
}

type loginAuth struct {
	username string
	password string
}

func (auth loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, ErrTLSIdentity
	}
	return "LOGIN", []byte(auth.username), nil
}

func (auth loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	challenge := strings.ToLower(string(fromServer))
	if strings.Contains(challenge, "username") {
		return []byte(auth.username), nil
	}
	if strings.Contains(challenge, "password") {
		return []byte(auth.password), nil
	}
	return nil, ErrProtocol
}

func smtpAuthenticator(mechanisms, username, password, host string) (smtp.Auth, error) {
	for _, mechanism := range strings.Fields(strings.ToUpper(mechanisms)) {
		switch mechanism {
		case "PLAIN":
			return smtp.PlainAuth("", username, password, host), nil
		case "LOGIN":
			return loginAuth{username: username, password: password}, nil
		}
	}
	return nil, ErrCapability
}

func (prober *NetworkProber) dial(ctx context.Context, server ServerConfig) (net.Conn, error) {
	deadline := time.Now().Add(prober.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	dialer := &net.Dialer{Timeout: time.Until(deadline)}
	address := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if err := connection.SetDeadline(deadline); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if server.TLSMode != TLSImplicit {
		return connection, nil
	}
	tlsConnection := tls.Client(connection, prober.tlsConfig(server.Host))
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return tlsConnection, nil
}

func (prober *NetworkProber) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: prober.roots}
}

func expectIMAPGreeting(reader *bufio.Reader) error {
	line, err := readBoundedLine(reader)
	if err != nil {
		return classifyConnectionError(context.Background(), err)
	}
	if !strings.HasPrefix(strings.ToUpper(line), "* OK") {
		return ErrProtocol
	}
	return nil
}

func writeIMAPCommand(writer *bufio.Writer, command string) error {
	if _, err := writer.WriteString(command + "\r\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func readIMAPResponse(reader *bufio.Reader, tag string, authenticating bool) ([]string, error) {
	lines := make([]string, 0, 8)
	for range 128 {
		line, err := readBoundedLine(reader)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, tag+" ") {
			continue
		}
		if strings.HasPrefix(upper, tag+" OK") {
			return lines, nil
		}
		if authenticating {
			return nil, ErrAuthentication
		}
		return nil, ErrProtocol
	}
	return nil, ErrProtocol
}

func readBoundedLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", ErrProtocol
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(line), "\r\n"), nil
}

func quoteIMAP(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

func imapCapabilities(lines []string, mode TLSMode) map[string]bool {
	result := map[string]bool{"imap.tls": true, "imap.starttls": mode == TLSStartTLS}
	allowed := map[string]string{"IDLE": "imap.idle", "UIDPLUS": "imap.uidplus", "MOVE": "imap.move", "CONDSTORE": "imap.condstore", "QRESYNC": "imap.qresync", "UTF8=ACCEPT": "imap.utf8_accept"}
	for _, line := range lines {
		fields := strings.Fields(strings.ToUpper(line))
		if len(fields) < 3 || fields[0] != "*" || fields[1] != "CAPABILITY" {
			continue
		}
		for _, capability := range fields[2:] {
			if key := allowed[capability]; key != "" {
				result[key] = true
			}
		}
	}
	return result
}

func classifyConnectionError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrTimeout
	}
	var hostnameError x509.HostnameError
	var authorityError x509.UnknownAuthorityError
	var certificateError x509.CertificateInvalidError
	if errors.As(err, &hostnameError) || errors.As(err, &authorityError) || errors.As(err, &certificateError) {
		return ErrTLSIdentity
	}
	return ErrProtocol
}
