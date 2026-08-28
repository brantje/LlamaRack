package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestWebSocketTicketsAreOneTimeAndBoundedPerSession(t *testing.T) {
	s := testService(t)
	user, err := s.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.CreateBearerSession(t.Context(), user, "127.0.0.1", "ticket-test")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.AuthenticateBearer(t.Context(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	tickets := make([]string, 0, maxWebSocketTicketsPerSession+1)
	for i := 0; i < maxWebSocketTicketsPerSession+1; i++ {
		ticket, _, err := s.IssueWebSocketTicket(t.Context(), session)
		if err != nil {
			t.Fatal(err)
		}
		tickets = append(tickets, ticket)
	}

	s.ticketMu.Lock()
	if got := len(s.wsTickets); got != maxWebSocketTicketsPerSession {
		s.ticketMu.Unlock()
		t.Fatalf("ticket count=%d want=%d", got, maxWebSocketTicketsPerSession)
	}
	_, newestPresent := s.wsTickets[tickets[len(tickets)-1]]
	s.ticketMu.Unlock()
	if !newestPresent {
		t.Fatal("newest websocket ticket was unexpectedly evicted")
	}
	if _, _, err := s.ConsumeWebSocketTicket(t.Context(), tickets[0]); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("oldest ticket should be evicted, got %v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(t.Context(), tickets[len(tickets)-1]); err != nil {
		t.Fatalf("newest ticket should be usable: %v", err)
	}
	if _, _, err := s.ConsumeWebSocketTicket(t.Context(), tickets[len(tickets)-1]); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("consumed ticket should be one-time, got %v", err)
	}
}

func TestWebSocketTicketsHaveGlobalBound(t *testing.T) {
	s := testService(t)
	user, err := s.Bootstrap(t.Context(), "admin", "correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.CreateBearerSession(t.Context(), user, "127.0.0.1", "ticket-test")
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.AuthenticateBearer(t.Context(), login.AccessToken)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	s.ticketMu.Lock()
	for i := 0; i < maxWebSocketTicketsGlobal; i++ {
		s.wsTickets[fmt.Sprintf("seed-%04d", i)] = wsTicket{
			SessionID: fmt.Sprintf("other-%04d", i),
			JTI:       "seed",
			ExpiresAt: now.Add(time.Minute + time.Duration(i)*time.Millisecond),
		}
	}
	s.ticketMu.Unlock()

	ticket, _, err := s.IssueWebSocketTicket(t.Context(), session)
	if err != nil {
		t.Fatal(err)
	}
	s.ticketMu.Lock()
	defer s.ticketMu.Unlock()
	if got := len(s.wsTickets); got != maxWebSocketTicketsGlobal {
		t.Fatalf("global ticket count=%d want=%d", got, maxWebSocketTicketsGlobal)
	}
	if _, ok := s.wsTickets[ticket]; !ok {
		t.Fatal("newly issued ticket missing after global eviction")
	}
	if _, ok := s.wsTickets["seed-0000"]; ok {
		t.Fatal("oldest global ticket was not evicted")
	}
}
