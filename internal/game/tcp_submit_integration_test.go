//go:build integration

package game

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// a farm keeps one tcp session open for hours: stopping the game must reach it
// without a reconnect, within the phase recheck interval.
func TestIntegrationTCPSessionSeesStop(t *testing.T) {
	db := testDB(t)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	token := strings.Repeat("cd", 32)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO game_teams (id, name, slug, team_number, token) VALUES ($1, 'tcp', 'tcp', 1, $2)`,
		uuid.New(), token); err != nil {
		t.Fatalf("team: %v", err)
	}
	setSetting(t, db, "game.state", `"running"`)
	setSetting(t, db, "event.start_at", fmt.Sprintf("%q", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)))
	setSetting(t, db, "event.end_at", fmt.Sprintf("%q", time.Now().Add(time.Hour).UTC().Format(time.RFC3339)))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	go NewTCPSubmitServer(db, zap.NewNop(), port).Run(ctx)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		if conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("banner: %v", err)
		}
		if line == "\n" {
			break
		}
	}
	send := func(flag string) string {
		t.Helper()
		if _, err := fmt.Fprintf(conn, "%s\n", flag); err != nil {
			t.Fatal(err)
		}
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reply: %v", err)
		}
		return line
	}
	if _, err := fmt.Fprintf(conn, "%s\n", token); err != nil {
		t.Fatal(err)
	}
	live := "H7CTF{" + strings.Repeat("0", 32) + "}"
	if reply := send(live); !strings.Contains(reply, " INV ") {
		t.Fatalf("live session reply = %q, want INV", reply)
	}
	setSetting(t, db, "game.state", `"stopped"`)
	time.Sleep(tcpPhaseRecheck + 200*time.Millisecond)
	if reply := send("H7CTF{" + strings.Repeat("1", 32) + "}"); !strings.Contains(reply, "ERR game not live") {
		t.Fatalf("after stop reply = %q, want ERR game not live", reply)
	}
}
