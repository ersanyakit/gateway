package realtime

import "testing"

func TestCleanToken(t *testing.T) {
	if got := cleanToken(" token "); got != "token" {
		t.Fatalf("cleanToken = %q", got)
	}
	if got := cleanToken("   "); got != "" {
		t.Fatalf("blank cleanToken = %q", got)
	}
}

func TestPaymentHubAddRemoveAndClients(t *testing.T) {
	hub := NewPaymentHub()
	client := &paymentClient{}
	hub.add("token", client)
	clients := hub.clients("token")
	if len(clients) != 1 || clients[0] != client {
		t.Fatalf("clients = %#v", clients)
	}
	hub.remove("token", client)
	if clients := hub.clients("token"); len(clients) != 0 {
		t.Fatalf("clients after remove = %#v", clients)
	}
	if _, ok := hub.rooms["token"]; ok {
		t.Fatal("empty room should be deleted")
	}
}
