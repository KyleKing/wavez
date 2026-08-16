package models_test

import (
	"testing"
	"time"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

func TestRelativeTimeJustNow(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now())
	if result != "just now" {
		t.Errorf("expected 'just now', got '%s'", result)
	}
}

func TestRelativeTimeMinutes(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-5 * time.Minute))
	if result != "5 mins ago" {
		t.Errorf("expected '5 mins ago', got '%s'", result)
	}

	result = models.RelativeTime(time.Now().Add(-1 * time.Minute))
	if result != "1 min ago" {
		t.Errorf("expected '1 min ago', got '%s'", result)
	}
}

func TestRelativeTimeHours(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-3 * time.Hour))
	if result != "3 hours ago" {
		t.Errorf("expected '3 hours ago', got '%s'", result)
	}

	result = models.RelativeTime(time.Now().Add(-1 * time.Hour))
	if result != "1 hour ago" {
		t.Errorf("expected '1 hour ago', got '%s'", result)
	}
}

func TestRelativeTimeDays(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-2 * 24 * time.Hour))
	if result != "2 days ago" {
		t.Errorf("expected '2 days ago', got '%s'", result)
	}

	result = models.RelativeTime(time.Now().Add(-1 * 24 * time.Hour))
	if result != "1 day ago" {
		t.Errorf("expected '1 day ago', got '%s'", result)
	}
}

func TestRelativeTimeWeeks(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-14 * 24 * time.Hour))
	if result != "2 weeks ago" {
		t.Errorf("expected '2 weeks ago', got '%s'", result)
	}

	result = models.RelativeTime(time.Now().Add(-7 * 24 * time.Hour))
	if result != "1 week ago" {
		t.Errorf("expected '1 week ago', got '%s'", result)
	}
}

func TestRelativeTimeMonths(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-60 * 24 * time.Hour))
	if result != "2 months ago" {
		t.Errorf("expected '2 months ago', got '%s'", result)
	}
}

func TestRelativeTimeYears(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Now().Add(-730 * 24 * time.Hour))
	if result != "2 years ago" {
		t.Errorf("expected '2 years ago', got '%s'", result)
	}
}

func TestRelativeTimeZero(t *testing.T) {
	t.Parallel()
	result := models.RelativeTime(time.Time{})
	if result != models.EmDash {
		t.Errorf("expected '—', got '%s'", result)
	}
}

func TestBranchInfoRelativeLastCommit(t *testing.T) {
	t.Parallel()
	b := models.BranchInfo{}
	if b.RelativeLastCommit() != models.EmDash {
		t.Errorf("expected '—' for zero time, got '%s'", b.RelativeLastCommit())
	}

	b.LastCommit = time.Now()
	if b.RelativeLastCommit() == models.EmDash {
		t.Error("expected non-empty relative time")
	}
}

func TestCommitInfoRelativeDate(t *testing.T) {
	t.Parallel()
	c := models.CommitInfo{Date: time.Now()}
	if c.RelativeDate() == models.EmDash {
		t.Error("expected non-empty relative date")
	}
}

func TestStashDetailRelativeDate(t *testing.T) {
	t.Parallel()
	s := models.StashDetail{Date: time.Now()}
	if s.RelativeDate() == models.EmDash {
		t.Error("expected non-empty relative date")
	}
}
