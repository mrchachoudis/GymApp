package llm

import "testing"

func TestStripEmoji(t *testing.T) {
	in := "Logged. 💪 Bench 100x5 is a PR 🔥🎯"
	got := StripEmoji(in)
	want := "Logged. Bench 100x5 is a PR"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStripEmojiKeepsOrdinaryPunctuation(t *testing.T) {
	in := "Solid session - 3 sets, no drama. Add 2.5 kg next time (bench)."
	if got := StripEmoji(in); got != in {
		t.Fatalf("punctuation should survive, got %q", got)
	}
}

// The banned-metaphor list is the deterministic half of a rule the prompt can
// only ask for politely.
func TestCheckReplyCatchesAquaticMetaphors(t *testing.T) {
	cases := []string{
		"Time to dive into the next block.",
		"You're a big fish now.",
		"Sink or swim on that last set.",
	}
	for _, c := range cases {
		if _, bad := CheckReply(c); !bad {
			t.Fatalf("expected %q to be caught", c)
		}
	}
}

// Word-boundary matching keeps the filter from firing on innocent words that
// merely contain a banned string.
func TestCheckReplyDoesNotFireOnSubstrings(t *testing.T) {
	clean := []string{
		"Selfish programming aside, that was a good session.",
		"Your form held up, no issues.",
		"Add 2.5 kg and reset the reps.",
	}
	for _, c := range clean {
		if v, bad := CheckReply(c); bad {
			t.Fatalf("%q should be clean, was flagged as %s/%s", c, v.Kind, v.Term)
		}
	}
}

func TestCheckReplyCatchesBannedOpeners(t *testing.T) {
	if _, bad := CheckReply("Great job today, that was solid."); !bad {
		t.Fatal("banned opener should be caught")
	}
	// The same phrase later in the reply is not an opener and should pass.
	if _, bad := CheckReply("Logged.\nThat top set was the good job of the day."); bad {
		t.Fatal("only the first line counts as the opener")
	}
}

func TestStripFencesHandlesFencedJSON(t *testing.T) {
	in := "```json\n{\"a\": 1}\n```"
	if got := StripFences(in); got != `{"a": 1}` {
		t.Fatalf("got %q", got)
	}
}

func TestStripFencesHandlesPreamble(t *testing.T) {
	in := `Here is the JSON you asked for: {"a": 1} Let me know if you need more.`
	if got := StripFences(in); got != `{"a": 1}` {
		t.Fatalf("got %q", got)
	}
}

func TestStubParsesCommonShapes(t *testing.T) {
	out, err := Stub{}.Complete(nil, CompletionRequest{
		System: "You are a strength-training log parser.",
		User:   "bench press 100 x 5, 5, 4; dips bw x 12, 10",
	})
	if err != nil {
		t.Fatalf("stub: %v", err)
	}
	for _, want := range []string{`"bench press"`, `"dips"`, `"bodyweight"`} {
		if !contains(out, want) {
			t.Fatalf("expected %s in %s", want, out)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
