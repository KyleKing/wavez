package srs_test

import (
	"math"
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/srs"
)

const epsilon = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestNewCard_DueImmediately(t *testing.T) {
	t.Parallel()
	now := time.Now()
	card := srs.NewCard()

	if !card.IsDue(now) {
		t.Errorf("NewCard().IsDue(now) = false; want true")
	}
}

func TestReview_GoodProgression(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := srs.NewCard()

	card = srs.Review(card, srs.GradeGood, now)
	if card.IntervalDays != 1 {
		t.Errorf("after 1st Good, IntervalDays = %d; want 1", card.IntervalDays)
	}

	card = srs.Review(card, srs.GradeGood, now)
	if card.IntervalDays != 6 {
		t.Errorf("after 2nd Good, IntervalDays = %d; want 6", card.IntervalDays)
	}

	prevInterval := card.IntervalDays
	prevEF := card.EaseFactor
	card = srs.Review(card, srs.GradeGood, now)
	wantInterval := int(math.Round(float64(prevInterval) * prevEF))
	if card.IntervalDays != wantInterval {
		t.Errorf("after 3rd Good, IntervalDays = %d; want %d", card.IntervalDays, wantInterval)
	}
}

func TestReview_AgainResetsIntervalNotEaseFloor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := srs.NewCard()

	card = srs.Review(card, srs.GradeGood, now)
	card = srs.Review(card, srs.GradeGood, now)
	card = srs.Review(card, srs.GradeGood, now)

	efBeforeLapse := card.EaseFactor
	if efBeforeLapse <= 1.3 {
		t.Fatalf("expected EaseFactor > floor before lapse, got %f", efBeforeLapse)
	}

	card = srs.Review(card, srs.GradeAgain, now)

	if card.Repetitions != 0 {
		t.Errorf("after Again, Repetitions = %d; want 0", card.Repetitions)
	}
	if card.IntervalDays != 1 {
		t.Errorf("after Again, IntervalDays = %d; want 1", card.IntervalDays)
	}
	if card.EaseFactor < 1.3 {
		t.Errorf("after Again, EaseFactor = %f; want >= 1.3 floor", card.EaseFactor)
	}
	if card.EaseFactor >= efBeforeLapse {
		t.Errorf(
			"after Again, EaseFactor = %f; want it to decrease from %f",
			card.EaseFactor,
			efBeforeLapse,
		)
	}
}

func TestReview_EaseFactorNeverBelowFloor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := srs.NewCard()

	for range 50 {
		card = srs.Review(card, srs.GradeAgain, now)
		if card.EaseFactor < 1.3 {
			t.Fatalf("EaseFactor dropped below floor: %f", card.EaseFactor)
		}
	}

	if !almostEqual(card.EaseFactor, 1.3) {
		t.Errorf("EaseFactor = %f; want floor of 1.3 after repeated Again", card.EaseFactor)
	}
}

func TestReview_EaseFactorExactValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := srs.NewCard()

	tests := []struct {
		name   string
		grade  srs.Grade
		wantEF float64
	}{
		{name: "Easy from initial 2.5", grade: srs.GradeEasy, wantEF: 2.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := srs.Review(card, tt.grade, now)
			if !almostEqual(got.EaseFactor, tt.wantEF) {
				t.Errorf("EaseFactor = %f; want %f", got.EaseFactor, tt.wantEF)
			}
		})
	}

	good := srs.Review(card, srs.GradeGood, now)
	if !almostEqual(good.EaseFactor, 2.5) {
		t.Errorf("Good from initial EaseFactor = %f; want 2.5 (unchanged)", good.EaseFactor)
	}

	hard := srs.Review(card, srs.GradeHard, now)
	if !almostEqual(hard.EaseFactor, 2.36) {
		t.Errorf("Hard from initial EaseFactor = %f; want 2.36", hard.EaseFactor)
	}

	again := srs.Review(card, srs.GradeAgain, now)
	if !almostEqual(again.EaseFactor, 1.7) {
		t.Errorf("Again from initial EaseFactor = %f; want 1.7", again.EaseFactor)
	}
}

func TestIsDue(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	card := srs.Review(srs.NewCard(), srs.GradeGood, now)

	tests := []struct {
		at   time.Time
		name string
		want bool
	}{
		{name: "before due date", at: now, want: false},
		{name: "day before due date", at: card.Due.AddDate(0, 0, -1), want: false},
		{name: "exactly on due date", at: card.Due, want: true},
		{name: "after due date", at: card.Due.AddDate(0, 0, 1), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := card.IsDue(tt.at); got != tt.want {
				t.Errorf("IsDue(%v) = %v; want %v", tt.at, got, tt.want)
			}
		})
	}
}
