package op

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
)

func TestStatusTokenLifecycleAndBackupRedaction(t *testing.T) {
	if err := db.InitDB("sqlite", t.TempDir()+"/status-token.db", false); err != nil {
		t.Fatalf("initialize database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	token, plaintext, err := StatusTokenCreate(t.Context(), "desktop", 0)
	if err != nil {
		t.Fatalf("create status token: %v", err)
	}
	if plaintext == "" || strings.Contains(token.TokenHash, plaintext) || token.TokenHash == plaintext {
		t.Fatalf("status token was not hashed: token=%+v plaintext=%q", token, plaintext)
	}
	if _, err := StatusTokenAuthenticate(t.Context(), plaintext); err != nil {
		t.Fatalf("authenticate created token: %v", err)
	}
	listed, err := StatusTokenList(t.Context())
	if err != nil || len(listed) != 1 {
		t.Fatalf("list status tokens = %+v, err = %v", listed, err)
	}

	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatalf("export database: %v", err)
	}
	encoded, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("encode backup: %v", err)
	}
	if strings.Contains(string(encoded), plaintext) || len(dump.StatusTokens) != 1 {
		t.Fatalf("backup leaked plaintext or omitted metadata: %s", encoded)
	}

	if err := StatusTokenRevoke(t.Context(), token.ID); err != nil {
		t.Fatalf("revoke status token: %v", err)
	}
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatalf("import older active-token backup: %v", err)
	}
	if _, err := StatusTokenAuthenticate(t.Context(), plaintext); err == nil {
		t.Fatal("revoked status token was accepted")
	}
	if StatusTokenValid(t.Context(), token.ID) {
		t.Fatal("revoked status token was reported valid")
	}
}
