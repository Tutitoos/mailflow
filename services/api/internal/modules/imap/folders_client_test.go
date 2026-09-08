package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

func TestNetworkProberDiscoversSpecialUseNamespaceAndFolderStatus(t *testing.T) {
	serverTLS, roots := probeCertificate(t)
	address := serveFolderDiscoveryIMAP(t, serverTLS)
	host, port, _ := net.SplitHostPort(address)
	prober, _ := NewNetworkProber(2*time.Second, roots)
	credentials := storedCredentials{
		Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: hostForTLS(host), Port: integerPort(port), TLSMode: TLSImplicit},
	}

	folders, err := prober.Discover(context.Background(), credentials)
	if err != nil {
		t.Fatalf("discover folders: %v", err)
	}
	if len(folders) != 9 {
		t.Fatalf("folder count = %d, want 9: %+v", len(folders), folders)
	}
	byName := make(map[string]DiscoveredFolder, len(folders))
	for _, folder := range folders {
		byName[folder.Name] = folder
	}
	if inbox := byName["INBOX"]; inbox.Role != mail.MailboxInbox || !inbox.Selectable || inbox.UIDValidity == nil || *inbox.UIDValidity != 101 || inbox.TotalCount != 10 || inbox.UnreadCount != 2 {
		t.Fatalf("inbox = %+v", inbox)
	}
	if sent := byName["Sent"]; sent.Role != mail.MailboxSent || !sent.Subscribed {
		t.Fatalf("sent = %+v", sent)
	}
	if parent := byName["Projects"]; parent.Selectable || parent.UIDValidity != nil || parent.UIDNext != nil {
		t.Fatalf("nonselectable parent = %+v", parent)
	}
	if custom := byName["Projects/日本語"]; custom.NamespacePrefix != "" || custom.Delimiter != "/" || !custom.Subscribed {
		t.Fatalf("custom folder = %+v", custom)
	}
}

func TestFolderParsingRejectsAmbiguousRolesAndKeepsIdentityAcrossDelimiters(t *testing.T) {
	if _, ok := parseListResponse(`* LIST (\Sent \Trash) "/" "conflict"`, "LIST"); ok {
		t.Fatal("conflicting special-use roles were accepted")
	}
	if got := decodeModifiedUTF7("Projects/&broken"); got != "" {
		t.Fatalf("malformed modified UTF-7 = %q", got)
	}
	first := []DiscoveredFolder{{WireName: "Projects/Invoices", Name: "Projects/Invoices", Delimiter: "/", Selectable: true, UIDNext: int64Pointer(2), UIDValidity: int64Pointer(42)}}
	second := []DiscoveredFolder{{WireName: "Projects.Invoices", Name: "Projects.Invoices", Delimiter: ".", Selectable: true, UIDNext: int64Pointer(2), UIDValidity: int64Pointer(42)}}
	if normalizeDiscoveredFolders(first) != nil || normalizeDiscoveredFolders(second) != nil || first[0].IdentityKey != second[0].IdentityKey {
		t.Fatalf("delimiter-stable identities = %q and %q", first[0].IdentityKey, second[0].IdentityKey)
	}
}

func TestFolderStatusRejectsCountersOutsideInt32Range(t *testing.T) {
	folder := DiscoveredFolder{}
	if err := applyStatus([]string{`* STATUS "INBOX" (MESSAGES 2147483648 UNSEEN 0 UIDNEXT 2 UIDVALIDITY 1)`}, &folder); !errors.Is(err, ErrFolderDiscovery) {
		t.Fatalf("oversized folder count error = %v", err)
	}
}

func serveFolderDiscoveryIMAP(t *testing.T, config *tls.Config) string {
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
			fields := strings.Fields(strings.TrimSpace(line))
			if len(fields) < 2 {
				return
			}
			tag, command := fields[0], strings.ToUpper(fields[1])
			switch command {
			case "LOGIN":
				writeFolderFixtureLines(writer, tag+" OK authenticated")
			case "NAMESPACE":
				writeFolderFixtureLines(writer, `* NAMESPACE (("" "/")) NIL NIL`, tag+" OK namespace")
			case "LIST":
				writeFolderFixtureLines(writer,
					`* LIST (\HasNoChildren) "/" "INBOX"`, `* LIST (\Sent) "/" "Sent"`, `* LIST (\Drafts) "/" "Drafts"`,
					`* LIST (\Trash) "/" "Trash"`, `* LIST (\Junk) "/" "Junk"`, `* LIST (\Archive) "/" "Archive"`,
					`* LIST (\All) "/" "All Mail"`, `* LIST (\Noselect \HasChildren) "/" "Projects"`,
					`* LIST (\HasNoChildren) "/" "Projects/&ZeVnLIqe-"`, tag+" OK list")
			case "LSUB":
				writeFolderFixtureLines(writer, `* LSUB () "/" "Sent"`, `* LSUB () "/" "Projects/&ZeVnLIqe-"`, tag+" OK lsub")
			case "STATUS":
				uid := int64(100) + statusSequence(fields[0])
				writeFolderFixtureLines(writer, fmt.Sprintf("* STATUS %s (MESSAGES 10 UNSEEN 2 UIDNEXT 11 UIDVALIDITY %d)", fields[2], uid), tag+" OK status")
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

func writeFolderFixtureLines(writer *bufio.Writer, lines ...string) {
	for _, line := range lines {
		_, _ = writer.WriteString(line + "\r\n")
	}
	_ = writer.Flush()
}

func statusSequence(tag string) int64 {
	var sequence int64
	_, _ = fmt.Sscanf(tag, "S%d", &sequence)
	return sequence
}

func int64Pointer(value int64) *int64 { return &value }
