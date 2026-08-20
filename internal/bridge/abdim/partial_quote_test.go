package abdim

import "testing"

func TestMessageQuoteReadsPartialQuoteFromCallback(t *testing.T) {
	quote := messageQuote(`{"quoteElem":{"quoteText":"selected","quoteOffset":3,"quoteMessage":{"clientMsgID":"client-1","serverMsgID":"server-1"}}}`)
	if quote == nil || quote.Text != "selected" || quote.Offset != 3 || quote.SourceClientMsgID != "client-1" || quote.SourceServerMsgID != "server-1" {
		t.Fatalf("messageQuote() = %#v", quote)
	}
}
