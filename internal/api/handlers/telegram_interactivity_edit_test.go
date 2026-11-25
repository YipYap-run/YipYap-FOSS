package handlers

import "testing"

func TestTelegramEditedText_Escapes(t *testing.T) {
	got := telegramEditedText("[critical] API <prod> & co\ndown", "Acknowledged by <jane>")
	want := "[critical] API &lt;prod&gt; &amp; co\ndown\n\nAcknowledged by &lt;jane&gt;"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if got := telegramEditedText("", "Resolved by user 7"); got != "Resolved by user 7" {
		t.Fatalf("got %q", got)
	}
}
