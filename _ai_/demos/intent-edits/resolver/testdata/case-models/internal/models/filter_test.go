package models_test

import (
	"testing"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

func TestActiveFilterNewActiveFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode        models.FilterMode
		wantEnabled bool
	}{
		{models.FilterModeAll, true},
		{models.FilterModeAhead, false},
		{models.FilterModeBehind, false},
		{models.FilterModeDirty, false},
		{models.FilterModeHasPR, false},
		{models.FilterModeHasStash, false},
	}

	for _, tt := range tests {
		t.Run(tt.mode.String(), func(t *testing.T) {
			t.Parallel()
			f := models.NewActiveFilter(tt.mode)
			if f.Enabled != tt.wantEnabled {
				t.Errorf("mode %s: expected enabled=%v, got %v", tt.mode, tt.wantEnabled, f.Enabled)
			}
			if f.Inverted {
				t.Error("new filter should not be inverted")
			}
			if f.Mode != tt.mode {
				t.Errorf("expected mode=%v, got %v", tt.mode, f.Mode)
			}
		})
	}
}

func TestActiveFilterDisplayName(t *testing.T) {
	t.Parallel()
	f := models.NewActiveFilter(models.FilterModeAhead)
	if f.DisplayName() != "Ahead" {
		t.Errorf("expected 'Ahead', got %q", f.DisplayName())
	}
}

func TestActiveFilterShortKey(t *testing.T) {
	t.Parallel()
	f := models.NewActiveFilter(models.FilterModeAhead)
	if f.ShortKey() != ">" {
		t.Errorf("expected '>', got %q", f.ShortKey())
	}
}

func TestSortDirectionString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		dir      models.SortDirection
		expected string
	}{
		{models.SortDirectionOff, ""},
		{models.SortDirectionAsc, "ASC"},
		{models.SortDirectionDesc, "DESC"},
	}

	for _, tt := range tests {
		result := tt.dir.String()
		if result != tt.expected {
			t.Errorf("SortDirection %d: expected %q, got %q", tt.dir, tt.expected, result)
		}
	}
}

func TestActiveSortNewActiveSort(t *testing.T) {
	t.Parallel()
	s := models.NewActiveSort(models.SortModeName, 0)
	if s.Mode != models.SortModeName {
		t.Errorf("expected SortModeName, got %v", s.Mode)
	}
	if s.Direction != models.SortDirectionOff {
		t.Error("new sort should have direction Off")
	}
	if s.Priority != 0 {
		t.Errorf("expected priority 0, got %d", s.Priority)
	}
}

func TestActiveSortIsEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		dir      models.SortDirection
		expected bool
	}{
		{models.SortDirectionOff, false},
		{models.SortDirectionAsc, true},
		{models.SortDirectionDesc, true},
	}

	for _, tt := range tests {
		s := models.ActiveSort{Direction: tt.dir}
		if s.IsEnabled() != tt.expected {
			t.Errorf("direction %v: expected IsEnabled()=%v, got %v", tt.dir, tt.expected, s.IsEnabled())
		}
	}
}

func TestActiveSortDisplayName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sort     models.ActiveSort
		expected string
	}{
		{
			sort:     models.ActiveSort{Mode: models.SortModeName, Direction: models.SortDirectionOff},
			expected: "Name",
		},
		{
			sort:     models.ActiveSort{Mode: models.SortModeName, Direction: models.SortDirectionAsc},
			expected: "Name (ASC)",
		},
		{
			sort:     models.ActiveSort{Mode: models.SortModeModified, Direction: models.SortDirectionDesc},
			expected: "Modified (DESC)",
		},
	}

	for _, tt := range tests {
		result := tt.sort.DisplayName()
		if result != tt.expected {
			t.Errorf("expected %q, got %q", tt.expected, result)
		}
	}
}

func TestActiveSortShortKey(t *testing.T) {
	t.Parallel()
	s := models.ActiveSort{Mode: models.SortModeName}
	if s.ShortKey() != "n" {
		t.Errorf("expected 'n', got %q", s.ShortKey())
	}
}
