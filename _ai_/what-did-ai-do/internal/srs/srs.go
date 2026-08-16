// Package srs implements spaced-repetition scheduling using the SM-2
// algorithm (as used by SuperMemo 2 and, with minor variations, Anki).
package srs

import (
	"math"
	"time"
)

// Grade is a simplified pass/fail-oriented rating a quiz UI can present as a
// small set of keybindings, mirroring Anki's four-button review scheme.
type Grade int

const (
	// GradeAgain marks a total failure: the answer was forgotten or wrong.
	GradeAgain Grade = iota
	// GradeHard marks a correct answer recalled only with real difficulty.
	GradeHard
	// GradeGood marks a correct answer recalled with some hesitation.
	GradeGood
	// GradeEasy marks a correct answer recalled instantly and confidently.
	GradeEasy
)

// SM-2's response-quality scale runs 0-5. The 1 and 2 values
// (incorrect-but-recognized) have no natural pass/fail analog, so Grade
// skips them: GradeAgain maps to the harshest failure (qualityFail) and the
// three passing grades spread across the top of the scale.
const (
	qualityFail = 0
	qualityHard = 3
	qualityGood = 4
	qualityEasy = 5
)

// quality maps a Grade onto SM-2's response-quality scale.
func (g Grade) quality() float64 {
	switch g {
	case GradeAgain:
		return qualityFail
	case GradeHard:
		return qualityHard
	case GradeGood:
		return qualityGood
	case GradeEasy:
		return qualityEasy
	default:
		return qualityHard
	}
}

// minEaseFactor is SM-2's floor on EaseFactor. Without a floor, repeated
// failures could drive the ease so low that intervals would stop growing
// even after the card starts being answered correctly again.
const minEaseFactor = 1.3

const initialEaseFactor = 2.5

// passingQuality is SM-2's threshold below which a response counts as a
// failure that resets the repetition streak.
const passingQuality = qualityHard

// Coefficients from SM-2's EaseFactor update formula:
// EF' = EF + (efBase - (maxQuality-q) * (efQualityCoeff + (maxQuality-q)*efQualitySquaredCoeff)).
const (
	maxQuality            = qualityEasy
	efBase                = 0.1
	efQualityCoeff        = 0.08
	efQualitySquaredCoeff = 0.02
)

const (
	firstIntervalDays  = 1
	secondIntervalDays = 6
)

const (
	firstReview  = 1
	secondReview = 2
)

// Card holds the SM-2 scheduling state for a single quiz item.
type Card struct {
	Due          time.Time
	LastReviewed time.Time
	EaseFactor   float64
	IntervalDays int
	Repetitions  int
}

// NewCard returns a Card in its initial state, due immediately.
func NewCard() Card {
	return Card{
		EaseFactor: initialEaseFactor,
		Due:        time.Time{},
	}
}

// IsDue reports whether the card should be reviewed as of now.
func (c Card) IsDue(now time.Time) bool {
	return !c.Due.After(now)
}

// Review applies a grade to the card as of now and returns the updated card
// with its next Due date computed per SM-2.
func Review(card Card, grade Grade, now time.Time) Card {
	q := grade.quality()

	qualityGap := maxQuality - q
	ef := card.EaseFactor + (efBase - qualityGap*(efQualityCoeff+qualityGap*efQualitySquaredCoeff))
	if ef < minEaseFactor {
		ef = minEaseFactor
	}

	repetitions := card.Repetitions
	interval := card.IntervalDays

	// A failing grade resets the repetition streak so the next few
	// intervals re-derive from scratch, but EF is left as computed above:
	// SM-2 treats ease and streak length as independent signals, so a
	// single lapse shouldn't erase the accumulated evidence that a card is
	// generally easy or hard.
	if q < passingQuality {
		repetitions = 0
		interval = firstIntervalDays
	} else {
		repetitions++
		switch repetitions {
		case firstReview:
			interval = firstIntervalDays
		case secondReview:
			interval = secondIntervalDays
		default:
			interval = int(math.Round(float64(interval) * ef))
		}
	}

	return Card{
		EaseFactor:   ef,
		IntervalDays: interval,
		Repetitions:  repetitions,
		Due:          now.AddDate(0, 0, interval),
		LastReviewed: now,
	}
}
