package imap

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"sort"
	"strings"
	"time"

	goimap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

const (
	defaultOperationTimeout = 2 * time.Minute
	maxSnapshotUIDs         = 1_000_000
)

type NetworkProtocol struct {
	timeout time.Duration
	roots   *x509.CertPool
}

func NewNetworkProtocol(timeout time.Duration, roots *x509.CertPool) (*NetworkProtocol, error) {
	if timeout <= 0 || timeout > 10*time.Minute {
		return nil, ErrInvalidConfiguration
	}
	return &NetworkProtocol{timeout: timeout, roots: roots}, nil
}

func DefaultNetworkProtocol() *NetworkProtocol {
	protocol, _ := NewNetworkProtocol(defaultOperationTimeout, nil)
	return protocol
}

func (protocol *NetworkProtocol) Fetch(ctx context.Context, credentials storedCredentials, request FetchRequest) (FetchResult, error) {
	client, closeClient, err := protocol.openIMAP(ctx, credentials)
	if err != nil {
		return FetchResult{}, err
	}
	defer closeClient()
	selected, err := client.Select(request.Folder.WireName, &goimap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return FetchResult{}, classifyProtocolError(ctx, err)
	}
	if request.Folder.UIDValidity == nil || int64(selected.UIDValidity) != *request.Folder.UIDValidity {
		return FetchResult{}, ErrUIDValidityChanged
	}
	fromUID := request.FromUID
	if fromUID < 1 {
		fromUID = 1
	}
	criteria := &goimap.SearchCriteria{}
	if !request.Snapshot {
		uidSet := goimap.UIDSet{}
		uidSet.AddRange(goimap.UID(fromUID), 0)
		criteria.UID = []goimap.UIDSet{uidSet}
	}
	if request.After != nil {
		criteria.Since = request.After.UTC()
	}
	if request.Before != nil {
		criteria.Before = request.Before.UTC()
	}
	search, err := client.UIDSearch(criteria, &goimap.SearchOptions{ReturnAll: true}).Wait()
	if err != nil {
		return FetchResult{}, classifyProtocolError(ctx, err)
	}
	allUIDs := search.AllUIDs()
	if len(allUIDs) > maxSnapshotUIDs {
		return FetchResult{}, ErrProtocol
	}
	sort.Slice(allUIDs, func(left, right int) bool { return allUIDs[left] < allUIDs[right] })
	uids := allUIDs
	if request.Snapshot {
		start := sort.Search(len(allUIDs), func(index int) bool { return int64(allUIDs[index]) >= fromUID })
		uids = allUIDs[start:]
	}
	limit := request.Limit
	if limit < 1 || limit > imapPageSize {
		limit = imapPageSize
	}
	hasMore := len(uids) > limit
	if hasMore {
		uids = uids[:limit]
	}
	nextUID := int64(selected.UIDNext)
	if len(uids) == 0 {
		if nextUID < fromUID {
			nextUID = fromUID
		}
		result := FetchResult{NextUID: nextUID}
		if request.Snapshot {
			result.SnapshotUIDs = numericUIDs(allUIDs)
		}
		return result, nil
	}
	section := &goimap.FetchItemBodySection{Peek: true}
	command := client.Fetch(goimap.UIDSetNum(uids...), &goimap.FetchOptions{
		UID: true, Flags: true, InternalDate: true, RFC822Size: true, BodySection: []*goimap.FetchItemBodySection{section},
	})
	defer command.Close() //nolint:errcheck
	result := FetchResult{Messages: make([]FetchedMessage, 0, len(uids)), NextUID: int64(uids[len(uids)-1]) + 1, HasMore: hasMore}
	if request.Snapshot && !hasMore {
		result.SnapshotUIDs = numericUIDs(allUIDs)
	}
	for data := command.Next(); data != nil; data = command.Next() {
		message, collectErr := collectBoundedMessage(data, int64(mailMaxRawBytes()))
		if collectErr != nil {
			return FetchResult{}, collectErr
		}
		message.UIDValidity = int64(selected.UIDValidity)
		result.Messages = append(result.Messages, message)
	}
	if err := command.Close(); err != nil {
		return FetchResult{}, classifyProtocolError(ctx, err)
	}
	sort.Slice(result.Messages, func(left, right int) bool { return result.Messages[left].UID < result.Messages[right].UID })
	return result, nil
}

func (protocol *NetworkProtocol) SetFlags(ctx context.Context, credentials storedCredentials, location MessageLocation, add, remove []string) error {
	return protocol.withSelected(ctx, credentials, location, false, func(client *imapclient.Client) error {
		set := goimap.UIDSetNum(goimap.UID(location.UID))
		if len(add) > 0 {
			flags := stringFlags(add)
			if err := client.Store(set, &goimap.StoreFlags{Op: goimap.StoreFlagsAdd, Silent: true, Flags: flags}, nil).Close(); err != nil {
				return classifyProtocolError(ctx, err)
			}
		}
		if len(remove) > 0 {
			flags := stringFlags(remove)
			if err := client.Store(set, &goimap.StoreFlags{Op: goimap.StoreFlagsDel, Silent: true, Flags: flags}, nil).Close(); err != nil {
				return classifyProtocolError(ctx, err)
			}
		}
		return nil
	})
}

func (protocol *NetworkProtocol) Move(ctx context.Context, credentials storedCredentials, location MessageLocation, destination FolderState, capabilities map[string]bool) (MoveResult, error) {
	var result MoveResult
	err := protocol.withSelected(ctx, credentials, location, false, func(client *imapclient.Client) error {
		set := goimap.UIDSetNum(goimap.UID(location.UID))
		var validity uint32
		var destinationUIDs goimap.UIDSet
		if capabilities["imap.move"] && client.Caps().Has(goimap.CapMove) {
			moved, err := client.Move(set, destination.WireName).Wait()
			if err != nil {
				return classifyProtocolError(ctx, err)
			}
			movedUIDs, ok := moved.DestUIDs.(goimap.UIDSet)
			if !ok {
				return ErrMessagePersistence
			}
			validity, destinationUIDs = moved.UIDValidity, movedUIDs
		} else {
			if !capabilities["imap.uidplus"] || !client.Caps().Has(goimap.CapUIDPlus) {
				return ErrActionUnsupported
			}
			copied, err := client.Copy(set, destination.WireName).Wait()
			if err != nil {
				return classifyProtocolError(ctx, err)
			}
			validity, destinationUIDs = copied.UIDValidity, copied.DestUIDs
			if err := client.Store(set, &goimap.StoreFlags{Op: goimap.StoreFlagsAdd, Silent: true, Flags: []goimap.Flag{goimap.FlagDeleted}}, nil).Close(); err != nil {
				return classifyProtocolError(ctx, err)
			}
			if err := client.UIDExpunge(set).Close(); err != nil {
				return classifyProtocolError(ctx, err)
			}
		}
		uids, ok := destinationUIDs.Nums()
		if !ok || len(uids) != 1 || validity == 0 || uids[0] == 0 {
			return ErrMessagePersistence
		}
		result = MoveResult{UIDValidity: int64(validity), UID: int64(uids[0])}
		return nil
	})
	return result, err
}

func (protocol *NetworkProtocol) Append(ctx context.Context, credentials storedCredentials, folder FolderState, raw []byte, flags []string) (AppendResult, error) {
	client, closeClient, err := protocol.openIMAP(ctx, credentials)
	if err != nil {
		return AppendResult{}, err
	}
	defer closeClient()
	command := client.Append(folder.WireName, int64(len(raw)), &goimap.AppendOptions{Flags: stringFlags(flags), Time: time.Now().UTC()})
	if _, err := command.Write(raw); err != nil {
		_ = command.Close()
		return AppendResult{}, classifyProtocolError(ctx, err)
	}
	if err := command.Close(); err != nil {
		return AppendResult{}, classifyProtocolError(ctx, err)
	}
	data, err := command.Wait()
	if err != nil {
		return AppendResult{}, classifyProtocolError(ctx, err)
	}
	return AppendResult{UIDValidity: int64(data.UIDValidity), UID: int64(data.UID)}, nil
}

func (protocol *NetworkProtocol) MarkDeleted(ctx context.Context, credentials storedCredentials, location MessageLocation) error {
	return protocol.SetFlags(ctx, credentials, location, []string{"\\Deleted"}, nil)
}

func (protocol *NetworkProtocol) RawMessage(ctx context.Context, credentials storedCredentials, location MessageLocation) ([]byte, error) {
	var raw []byte
	err := protocol.withSelected(ctx, credentials, location, true, func(client *imapclient.Client) error {
		section := &goimap.FetchItemBodySection{Peek: true}
		command := client.Fetch(goimap.UIDSetNum(goimap.UID(location.UID)), &goimap.FetchOptions{UID: true, RFC822Size: true, BodySection: []*goimap.FetchItemBodySection{section}})
		defer command.Close() //nolint:errcheck
		data := command.Next()
		if data == nil {
			return ErrInvalidMessageLocation
		}
		message, collectErr := collectBoundedMessage(data, int64(mailMaxRawBytes()))
		if collectErr != nil {
			return collectErr
		}
		if command.Next() != nil {
			return ErrInvalidMessageLocation
		}
		if err := command.Close(); err != nil {
			return classifyProtocolError(ctx, err)
		}
		raw = message.Raw
		return nil
	})
	return raw, err
}

func (protocol *NetworkProtocol) SendSMTP(ctx context.Context, credentials storedCredentials, raw []byte) error {
	prober := &NetworkProber{timeout: protocol.timeout, roots: protocol.roots}
	connection, err := prober.dial(ctx, credentials.SMTP)
	if err != nil {
		return classifyConnectionError(ctx, err)
	}
	defer connection.Close()
	client, err := smtp.NewClient(connection, credentials.SMTP.Host)
	if err != nil {
		return classifyConnectionError(ctx, err)
	}
	defer client.Close()
	if credentials.SMTP.TLSMode == TLSStartTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			return ErrCapability
		}
		if err := client.StartTLS(prober.tlsConfig(credentials.SMTP.Host)); err != nil {
			return classifyConnectionError(ctx, err)
		}
	}
	advertised, mechanisms := client.Extension("AUTH")
	if !advertised {
		return ErrCapability
	}
	authenticator, err := smtpAuthenticator(mechanisms, credentials.Username, credentials.Password, credentials.SMTP.Host)
	if err != nil {
		return err
	}
	if err := client.Auth(authenticator); err != nil {
		return classifySMTPError(ctx, err)
	}
	from, recipients, err := smtpEnvelope(raw)
	if err != nil {
		return err
	}
	if err := client.Mail(from); err != nil {
		return classifySMTPError(ctx, err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return classifySMTPError(ctx, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return classifySMTPError(ctx, err)
	}
	deliveryPayload := withoutBccHeader(raw)
	if _, err := io.Copy(writer, bytes.NewReader(deliveryPayload)); err != nil {
		_ = writer.Close()
		return classifyConnectionError(ctx, err)
	}
	if err := writer.Close(); err != nil {
		return classifySMTPError(ctx, err)
	}
	return nil
}

func (protocol *NetworkProtocol) withSelected(ctx context.Context, credentials storedCredentials, location MessageLocation, readOnly bool, operation func(*imapclient.Client) error) error {
	client, closeClient, err := protocol.openIMAP(ctx, credentials)
	if err != nil {
		return err
	}
	defer closeClient()
	selected, err := client.Select(location.WireName, &goimap.SelectOptions{ReadOnly: readOnly}).Wait()
	if err != nil {
		return classifyProtocolError(ctx, err)
	}
	if location.UIDValidity < 1 || int64(selected.UIDValidity) != location.UIDValidity {
		return ErrUIDValidityChanged
	}
	return operation(client)
}

func (protocol *NetworkProtocol) openIMAP(ctx context.Context, credentials storedCredentials) (*imapclient.Client, func(), error) {
	prober := &NetworkProber{timeout: protocol.timeout, roots: protocol.roots}
	connection, err := prober.dial(ctx, credentials.IMAP)
	if err != nil {
		return nil, nil, classifyConnectionError(ctx, err)
	}
	options := &imapclient.Options{TLSConfig: prober.tlsConfig(credentials.IMAP.Host)}
	var client *imapclient.Client
	if credentials.IMAP.TLSMode == TLSStartTLS {
		client, err = imapclient.NewStartTLS(connection, options)
	} else {
		client = imapclient.New(connection, options)
	}
	if err != nil {
		_ = connection.Close()
		return nil, nil, classifyProtocolError(ctx, err)
	}
	if err := client.Login(credentials.Username, credentials.Password).Wait(); err != nil {
		_ = client.Close()
		return nil, nil, classifyProtocolError(ctx, err)
	}
	closeClient := func() {
		_ = client.Logout().Wait()
		_ = client.Close()
	}
	return client, closeClient, nil
}

func smtpEnvelope(raw []byte) (string, []string, error) {
	message, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", nil, ErrProtocol
	}
	fromValues, err := stdmail.ParseAddressList(message.Header.Get("From"))
	if err != nil || len(fromValues) != 1 {
		return "", nil, ErrProtocol
	}
	seen := map[string]bool{}
	var recipients []string
	for _, name := range []string{"To", "Cc", "Bcc"} {
		value := message.Header.Get(name)
		if value == "" {
			continue
		}
		addresses, parseErr := stdmail.ParseAddressList(value)
		if parseErr != nil {
			return "", nil, ErrProtocol
		}
		for _, address := range addresses {
			key := strings.ToLower(address.Address)
			if !seen[key] {
				recipients = append(recipients, address.Address)
				seen[key] = true
			}
		}
	}
	if len(recipients) == 0 {
		return "", nil, ErrProtocol
	}
	return fromValues[0].Address, recipients, nil
}

func withoutBccHeader(raw []byte) []byte {
	separator := []byte("\r\n\r\n")
	boundary := bytes.Index(raw, separator)
	if boundary < 0 {
		return raw
	}
	lines := bytes.Split(raw[:boundary], []byte("\r\n"))
	filtered := make([][]byte, 0, len(lines))
	skipping := false
	for _, line := range lines {
		continuation := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		if continuation && skipping {
			continue
		}
		skipping = !continuation && bytes.HasPrefix(bytes.ToLower(line), []byte("bcc:"))
		if !skipping {
			filtered = append(filtered, line)
		}
	}
	result := bytes.Join(filtered, []byte("\r\n"))
	result = append(result, separator...)
	result = append(result, raw[boundary+len(separator):]...)
	return result
}

func stringFlags(values []string) []goimap.Flag {
	result := make([]goimap.Flag, 0, len(values))
	for _, value := range values {
		if value != "" && !slices.Contains(result, goimap.Flag(value)) {
			result = append(result, goimap.Flag(value))
		}
	}
	return result
}

func numericUIDs(values []goimap.UID) []int64 {
	result := make([]int64, 0, len(values))
	for _, value := range values {
		result = append(result, int64(value))
	}
	return result
}

func collectBoundedMessage(data *imapclient.FetchMessageData, limit int64) (FetchedMessage, error) {
	var result FetchedMessage
	seenBody := false
	for item := data.Next(); item != nil; item = data.Next() {
		switch value := item.(type) {
		case imapclient.FetchItemDataUID:
			result.UID = int64(value.UID)
		case imapclient.FetchItemDataFlags:
			for _, flag := range value.Flags {
				result.Flags = append(result.Flags, string(flag))
			}
		case imapclient.FetchItemDataInternalDate:
			result.SentAt = value.Time.UTC()
		case imapclient.FetchItemDataRFC822Size:
			if value.Size < 0 || value.Size > limit {
				return FetchedMessage{}, ErrProtocol
			}
		case imapclient.FetchItemDataBodySection:
			if seenBody || value.Literal == nil {
				return FetchedMessage{}, ErrProtocol
			}
			payload, err := io.ReadAll(io.LimitReader(value.Literal, limit+1))
			if err != nil || int64(len(payload)) > limit {
				return FetchedMessage{}, ErrProtocol
			}
			result.Raw = payload
			seenBody = true
		}
	}
	if result.UID < 1 || !seenBody || len(result.Raw) == 0 {
		return FetchedMessage{}, ErrProtocol
	}
	return result, nil
}

func classifyProtocolError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	var protocolError *goimap.Error
	if errors.As(err, &protocolError) {
		if protocolError.Type == goimap.StatusResponseTypeNo || protocolError.Type == goimap.StatusResponseTypeBad {
			return ErrProtocol
		}
	}
	return classifyConnectionError(ctx, err)
}

func classifySMTPError(ctx context.Context, err error) error {
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		return ErrProtocol
	}
	return classifyConnectionError(ctx, err)
}

func mailMaxRawBytes() int { return 25 << 20 }

var _ Protocol = (*NetworkProtocol)(nil)
