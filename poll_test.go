package main

import (
	"testing"

	"github.com/gotd/td/tg"
)

func pollAnswer(text, option string) tg.PollAnswerClass {
	return &tg.PollAnswer{
		Text:   tg.TextWithEntities{Text: text},
		Option: []byte(option),
	}
}

// TestPollFromMedia checks that tallies are matched to options by their option
// bytes rather than by position: the two orders need not agree.
func TestPollFromMedia(t *testing.T) {
	media := &tg.MessageMediaPoll{
		Poll: tg.Poll{
			Question:       tg.TextWithEntities{Text: "Deploy today?"},
			Answers:        []tg.PollAnswerClass{pollAnswer("yes", "a"), pollAnswer("no", "b")},
			MultipleChoice: true,
		},
		Results: tg.PollResults{
			TotalVoters: 3,
			// Deliberately reversed relative to Answers.
			Results: []tg.PollAnswerVoters{
				{Option: []byte("b"), Voters: 1},
				{Option: []byte("a"), Voters: 2, Chosen: true},
			},
		},
	}

	got := pollFromMedia(media)

	if got.Question != "Deploy today?" {
		t.Errorf("question = %q, want %q", got.Question, "Deploy today?")
	}
	if !got.MultipleChoice {
		t.Error("multiple_choice = false, want true")
	}
	if got.TotalVoters != 3 {
		t.Errorf("total_voters = %d, want 3", got.TotalVoters)
	}
	if len(got.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(got.Options))
	}

	yes := got.Options[0]
	if yes.Index != 0 || yes.Text != "yes" || yes.Voters != 2 || !yes.Chosen {
		t.Errorf("option 0 = %+v, want index 0 text yes voters 2 chosen", yes)
	}
	no := got.Options[1]
	if no.Index != 1 || no.Text != "no" || no.Voters != 1 || no.Chosen {
		t.Errorf("option 1 = %+v, want index 1 text no voters 1 not chosen", no)
	}
}

// TestPollFromMediaWithoutResults covers a poll whose tally is hidden, where
// Results is empty and options must still be listed.
func TestPollFromMediaWithoutResults(t *testing.T) {
	media := &tg.MessageMediaPoll{
		Poll: tg.Poll{
			Question: tg.TextWithEntities{Text: "Pick one"},
			Answers:  []tg.PollAnswerClass{pollAnswer("a", "0"), pollAnswer("b", "1")},
		},
	}

	got := pollFromMedia(media)

	if len(got.Options) != 2 {
		t.Fatalf("options = %d, want 2", len(got.Options))
	}
	for i, o := range got.Options {
		if o.Index != i {
			t.Errorf("option %d has index %d", i, o.Index)
		}
		if o.Voters != 0 || o.Chosen {
			t.Errorf("option %d = %+v, want no tally", i, o)
		}
	}
}

func TestSendPollValidation(t *testing.T) {
	srv := &server{}
	idx := func(i int) *int { return &i }

	for _, tt := range []struct {
		name string
		in   sendPollInput
	}{
		{name: "no chat", in: sendPollInput{Question: "q", Options: []string{"a", "b"}}},
		{name: "no question", in: sendPollInput{Chat: "me", Options: []string{"a", "b"}}},
		{name: "one option", in: sendPollInput{Chat: "me", Question: "q", Options: []string{"a"}}},
		{
			name: "quiz without correct option",
			in:   sendPollInput{Chat: "me", Question: "q", Options: []string{"a", "b"}, Quiz: true},
		},
		{
			name: "correct option out of range",
			in: sendPollInput{
				Chat: "me", Question: "q", Options: []string{"a", "b"},
				Quiz: true, CorrectOption: idx(2),
			},
		},
		{
			name: "negative correct option",
			in: sendPollInput{
				Chat: "me", Question: "q", Options: []string{"a", "b"},
				Quiz: true, CorrectOption: idx(-1),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Validation runs before any network call, so a nil API is fine.
			if _, _, err := srv.handleSendPoll(t.Context(), nil, tt.in); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestHasInlineResult(t *testing.T) {
	results := []tg.BotInlineResultClass{
		&tg.BotInlineResult{ID: "first"},
		&tg.BotInlineResult{ID: "second"},
	}

	for _, tt := range []struct {
		id   string
		want bool
	}{
		{id: "first", want: true},
		{id: "second", want: true},
		{id: "third", want: false},
		{id: "", want: false},
	} {
		t.Run(tt.id, func(t *testing.T) {
			if got := hasInlineResult(results, tt.id); got != tt.want {
				t.Errorf("hasInlineResult(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
