package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNetworkProtocolOpenIMAPNegotiatesImplicitTLS(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	completed := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			completed <- acceptErr
			return
		}
		defer connection.Close()
		secure := tls.Server(connection, serverTLS)
		if handshakeErr := secure.Handshake(); handshakeErr != nil {
			completed <- handshakeErr
			return
		}
		reader, writer := bufio.NewReader(secure), bufio.NewWriter(secure)
		_, _ = writer.WriteString("* OK [CAPABILITY IMAP4rev1] fixture ready\r\n")
		_ = writer.Flush()
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				completed <- readErr
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				completed <- ErrProtocol
				return
			}
			tag, command := fields[0], strings.ToUpper(fields[1])
			switch command {
			case "LOGIN":
				_, _ = writer.WriteString(tag + " OK authenticated\r\n")
			case "CAPABILITY":
				_, _ = writer.WriteString("* CAPABILITY IMAP4rev1\r\n" + tag + " OK capability complete\r\n")
			case "LOGOUT":
				_, _ = writer.WriteString("* BYE closing\r\n" + tag + " OK logout complete\r\n")
				_ = writer.Flush()
				completed <- nil
				return
			default:
				completed <- ErrProtocol
				return
			}
			_ = writer.Flush()
		}
	}()

	host, portValue, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port := integerPort(portValue)
	protocol, err := NewNetworkProtocol(time.Second, roots)
	if err != nil {
		t.Fatal(err)
	}
	client, closeClient, err := protocol.openIMAP(context.Background(), storedCredentials{
		Username: "owner@example.test", Password: "fixture",
		IMAP: ServerConfig{Host: host, Port: port, TLSMode: TLSImplicit},
	})
	if err != nil || client == nil {
		t.Fatalf("open implicit TLS IMAP: client=%v error=%v", client, err)
	}
	closeClient()
	if err := <-completed; err != nil {
		t.Fatalf("serve implicit TLS IMAP: %v", err)
	}
}
