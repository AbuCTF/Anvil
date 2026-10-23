package game

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/anvil-lab/anvil/internal/database"
	"go.uber.org/zap"
)

const (
	tcpBanner      = "H7CTF Attack-Defense Flag Submission\nOne flag per line please\nEnter your flags\n\n"
	tcpReadTimeout = 30 * time.Second
	tcpMaxLineLen  = 512
	// how stale a session's view of the game phase may get
	tcpPhaseRecheck = time.Second
)

// TCPSubmitServer accepts flag submissions over a bare TCP connection.
// Protocol (01-ad-formats.md §4.4):
//   - Server sends a banner ending in \n\n; banner MUST contain both
//     "One flag per line please" and "Enter your flags".
//   - Client sends the team token on the first line, then one flag per
//     subsequent line (pipelined; no need to wait for a reply).
//   - Server replies "<flag> <CODE> <msg>\n" per flag:
//     OK / DUP / OWN / OLD / INV / NOP / ERR
type TCPSubmitServer struct {
	db     *database.DB
	logger *zap.Logger
	port   int
}

// NewTCPSubmitServer creates a TCPSubmitServer ready to Run.
func NewTCPSubmitServer(db *database.DB, logger *zap.Logger, port int) *TCPSubmitServer {
	return &TCPSubmitServer{db: db, logger: logger, port: port}
}

// Run listens and serves until ctx is cancelled.
func (s *TCPSubmitServer) Run(ctx context.Context) {
	addr := fmt.Sprintf(":%d", s.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.logger.Error("tcp submit: listen failed", zap.String("addr", addr), zap.Error(err))
		return
	}
	s.logger.Info("tcp submit: listening", zap.String("addr", addr))

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.logger.Warn("tcp submit: accept error", zap.Error(err))
				continue
			}
		}
		go s.handle(ctx, conn)
	}
}

func (s *TCPSubmitServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	// send banner
	if _, err := fmt.Fprint(conn, tcpBanner); err != nil {
		return
	}

	conn.SetDeadline(time.Now().Add(tcpReadTimeout))
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, tcpMaxLineLen), tcpMaxLineLen)

	// first line = team token
	if !sc.Scan() {
		return
	}
	tok := strings.TrimSpace(sc.Text())
	teamID, isNOP, found, err := TeamForToken(ctx, s.db, tok)
	if err != nil || !found {
		fmt.Fprintf(conn, "token ERR %s\n", "invalid team token")
		return
	}

	phase, ok, err := FinalsPhase(ctx, s.db)
	if err != nil {
		fmt.Fprintf(conn, "game ERR %s\n", "internal error")
		return
	}
	phaseAt := time.Now()

	// process flags pipelined
	for sc.Scan() {
		conn.SetDeadline(time.Now().Add(tcpReadTimeout))
		flag := strings.TrimSpace(sc.Text())
		if flag == "" {
			continue
		}

		if isNOP {
			fmt.Fprintf(conn, "%s NOP %s\n", flag, "nop team")
			continue
		}
		// a farm keeps one session open for hours: a pause or the end must reach it
		if time.Since(phaseAt) >= tcpPhaseRecheck {
			if phase, ok, err = FinalsPhase(ctx, s.db); err != nil {
				fmt.Fprintf(conn, "%s ERR %s\n", flag, "internal error")
				return
			}
			phaseAt = time.Now()
		}
		if !ok || phase != PhaseLive {
			fmt.Fprintf(conn, "%s ERR %s\n", flag, "game not live")
			continue
		}

		outcome, err := SubmitFlag(ctx, s.db, teamID, flag)
		if err != nil {
			s.logger.Error("tcp submit: SubmitFlag", zap.Error(err))
			fmt.Fprintf(conn, "%s ERR %s\n", flag, "internal error")
			continue
		}
		code := tcpCode(outcome)
		fmt.Fprintf(conn, "%s %s %s\n", flag, code, submitTCPMsg(outcome))
	}
}

func tcpCode(o SubmitOutcome) string {
	switch o {
	case SubmitAccepted:
		return "OK"
	case SubmitDuplicate:
		return "DUP"
	case SubmitOwnFlag:
		return "OWN"
	case SubmitExpired:
		return "OLD"
	case SubmitInvalid:
		return "INV"
	case SubmitNop:
		return "NOP"
	default:
		return "ERR"
	}
}

func submitTCPMsg(o SubmitOutcome) string {
	switch o {
	case SubmitAccepted:
		return "accepted"
	case SubmitDuplicate:
		return "already submitted"
	case SubmitOwnFlag:
		return "your own flag"
	case SubmitExpired:
		return "too old"
	case SubmitInvalid:
		return "invalid flag"
	case SubmitNop:
		return "nop team"
	default:
		return string(o)
	}
}
