package imap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/accounts"
)

type serviceAccounts struct {
	input   accounts.CreateInput
	account accounts.Account
	cleared bool
}

func (store *serviceAccounts) Connect(_ context.Context, input accounts.CreateInput) (accounts.Account, error) {
	store.input = input
	store.account = accounts.Account{ID: "account", Provider: input.Provider, RemoteID: input.RemoteID, DisplayName: input.DisplayName, Capabilities: input.Capabilities}
	return store.account, nil
}

func (store *serviceAccounts) Get(context.Context, string, string) (accounts.Account, error) {
	return store.account, nil
}

func (store *serviceAccounts) DisableAndClearCredentials(context.Context, string, string) (accounts.Account, error) {
	store.cleared = true
	store.account.SyncState = accounts.SyncDisabled
	store.account.Capabilities = map[string]bool{}
	return store.account, nil
}

type serviceProber struct {
	capabilities map[string]bool
	err          error
}

func (prober serviceProber) Probe(context.Context, ConnectInput) (map[string]bool, error) {
	return prober.capabilities, prober.err
}

func validConnectInput() ConnectInput {
	return ConnectInput{
		UserID: "0199ed3b-c950-7000-8000-000000000001", DisplayName: "Personal", Username: "owner@example.test", Password: "app-password",
		IMAP: ServerConfig{Host: "imap.example.test", Port: 993, TLSMode: TLSImplicit},
		SMTP: ServerConfig{Host: "smtp.example.test", Port: 587, TLSMode: TLSStartTLS},
	}
}

func TestServiceProbesFiltersCapabilitiesAndEncryptsThroughAccountStore(t *testing.T) {
	store := &serviceAccounts{}
	service, err := NewService(store, serviceProber{capabilities: map[string]bool{"imap.idle": true, "smtp.smtputf8": true, "private.transcript": true}})
	if err != nil {
		t.Fatal(err)
	}
	input := validConnectInput()
	account, err := service.Connect(context.Background(), input)
	if err != nil || account.Provider != accounts.ProviderIMAP || !account.Capabilities["imap.idle"] || !account.Capabilities["smtp.smtputf8"] || account.Capabilities["private.transcript"] {
		t.Fatalf("account=%+v error=%v", account, err)
	}
	if strings.Contains(account.RemoteID, "owner") || strings.Contains(account.RemoteID, "example") || len(account.RemoteID) != len("imap:")+64 {
		t.Fatalf("remote ID exposed identity: %q", account.RemoteID)
	}
	var stored storedCredentials
	if json.Unmarshal(store.input.Credentials, &stored) != nil || stored.Password != input.Password || stored.Username != input.Username {
		t.Fatal("credentials were not passed to the encrypted account repository")
	}
	encoded, _ := json.Marshal(account)
	if strings.Contains(string(encoded), input.Password) || strings.Contains(string(encoded), input.Username) {
		t.Fatal("account response exposed credentials")
	}
}

func TestServiceRejectsCleartextAndSeparatesProbeFailures(t *testing.T) {
	input := validConnectInput()
	input.IMAP.TLSMode = "cleartext"
	service, _ := NewService(&serviceAccounts{}, serviceProber{})
	if _, err := service.Probe(context.Background(), input); !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("cleartext error=%v", err)
	}
	input = validConnectInput()
	service, _ = NewService(&serviceAccounts{}, serviceProber{err: ErrTLSIdentity})
	if _, err := service.Connect(context.Background(), input); !errors.Is(err, ErrTLSIdentity) {
		t.Fatalf("TLS error=%v", err)
	}
}

func TestServiceDisconnectClearsOnlyIMAPCredentials(t *testing.T) {
	store := &serviceAccounts{account: accounts.Account{ID: "account", Provider: accounts.ProviderIMAP}}
	service, _ := NewService(store, serviceProber{})
	result, err := service.Disconnect(context.Background(), "owner", "account")
	if err != nil || !result.CredentialsRemoved || result.RemoteRevoked || !store.cleared || result.Account.SyncState != accounts.SyncDisabled {
		t.Fatalf("disconnect=%+v cleared=%v error=%v", result, store.cleared, err)
	}
	store.account.Provider = accounts.ProviderGoogle
	if _, err := service.Disconnect(context.Background(), "owner", "account"); !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("wrong provider error=%v", err)
	}
}
