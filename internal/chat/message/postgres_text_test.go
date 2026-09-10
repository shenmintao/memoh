package message

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	dbsqlc "github.com/felinics/memoh/internal/db/postgres/sqlc"
	postgresstore "github.com/felinics/memoh/internal/db/postgres/store"
)

func TestPostgresMessageJSONEscapesOnlyRealNUL(t *testing.T) {
	tests := []struct{ name, input, want string }{
		{"nested", `{"text":"a\u0000b","parts":[{"text":"\u0000"}]}`, `{"text":"a\u2400b","parts":[{"text":"\u2400"}]}`},
		{"literal", `{"text":"\\u0000"}`, `{"text":"\\u0000"}`},
		{"slash then NUL", `{"text":"\\\u0000"}`, `{"text":"\\\u2400"}`},
		{"precision and unicode", `{"n":9007199254740993,"f":1.234567890123456789,"text":"中文😀\n\t\u0000"}`, `{"n":9007199254740993,"f":1.234567890123456789,"text":"中文😀\n\t\u2400"}`},
		{"no changes", `{"text":"emoji \ud83d\ude00 and slash \\\"","empty":null}`, `{"text":"emoji \ud83d\ude00 and slash \\\"","empty":null}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.input)
			got := postgresMessageJSON(raw)
			if string(got) != tc.want || !json.Valid(got) {
				t.Fatalf("got %s want %s", got, tc.want)
			}
			if string(raw) != tc.input {
				t.Fatal("mutated caller input")
			}
			if !bytes.Equal(postgresMessageJSON(got), got) {
				t.Fatal("normalization not idempotent")
			}
		})
	}
}

func TestPostgresHistoryRoundPreservesReplyWithNUL(t *testing.T) {
	ctx := context.Background()
	tx := beginPostgresMessageTestTx(t, ctx)
	if _, err := tx.Exec(ctx, "SAVEPOINT nul_probe"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT $1::jsonb`, `{"text":"output\u0000tail"}`).Scan(&raw)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "22P05" {
		t.Fatalf("expected reported PostgreSQL failure, got %v", err)
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT nul_probe"); err != nil {
		t.Fatal(err)
	}
	setupPostgresMessageTestFixtures(t, ctx, tx)
	svc := NewService(nil, postgresstore.NewQueries(dbsqlc.New(tx)))
	user, err := svc.Persist(ctx, PersistInput{BotID: postgresMessageTestBotID, SessionID: postgresMessageTestSessionID, Role: "user", Content: []byte(`{"role":"user","content":"question"}`)})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := svc.Persist(ctx, PersistInput{
		BotID: postgresMessageTestBotID, SessionID: postgresMessageTestSessionID, Role: "assistant", TurnRequestMessageID: user.ID,
		Content:     []byte(`{"role":"assistant","content":"output\u0000tail","tool":{"text":"A\u0000B","literal":"\\u0000"},"n":9007199254740993}`),
		DisplayText: "output\x00tail", Metadata: map[string]any{"nested": map[string]any{"text": "A\x00B"}},
	})
	if err != nil {
		t.Fatalf("reply persistence still fails: %v", err)
	}
	if reply.DisplayContent != "output␀tail" {
		t.Fatalf("display=%q", reply.DisplayContent)
	}
	var content struct {
		Content string                         `json:"content"`
		Tool    struct{ Text, Literal string } `json:"tool"`
		N       json.Number                    `json:"n"`
	}
	if err := json.Unmarshal(reply.Content, &content); err != nil {
		t.Fatal(err)
	}
	if content.Content != "output␀tail" || content.Tool.Text != "A␀B" || content.Tool.Literal != `\u0000` || content.N.String() != "9007199254740993" {
		t.Fatalf("bad stored projection: %#v", content)
	}
	if reply.Metadata["nested"].(map[string]any)["text"] != "A␀B" {
		t.Fatal("metadata not normalized")
	}
	assertPostgresVisibleMessageIDs(t, ctx, svc, user.ID, reply.ID)
}
