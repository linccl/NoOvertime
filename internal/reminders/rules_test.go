package reminders

import (
	"strings"
	"testing"
	"time"
)

func TestBuildScheduleBoundaries(t *testing.T) {
	start := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	items := BuildSchedule(ScheduleRequest{
		UserID:            "user-1",
		StartPunchID:      "punch-1",
		StartPunchVersion: 7,
		LocalDate:         "2026-06-03",
		StartAtUTC:        start,
	})

	if len(items) != 11 {
		t.Fatalf("len(items) = %d", len(items))
	}

	first := items[0]
	if first.ReminderType != TypeEndReminder {
		t.Fatalf("first type = %q", first.ReminderType)
	}
	if first.AdjustMinutes != 0 {
		t.Fatalf("first adjust minutes = %d", first.AdjustMinutes)
	}
	if first.ScheduledAfterStartMinutes != 539 {
		t.Fatalf("first scheduled after start = %d", first.ScheduledAfterStartMinutes)
	}
	if !first.ScheduledAtUTC.Equal(start.Add(8*time.Hour + 59*time.Minute)) {
		t.Fatalf("first scheduled at = %s", first.ScheduledAtUTC)
	}

	second := items[1]
	if second.ReminderType != TypeAdjustReminder {
		t.Fatalf("second type = %q", second.ReminderType)
	}
	if second.AdjustMinutes != 30 {
		t.Fatalf("second adjust minutes = %d", second.AdjustMinutes)
	}
	if second.ScheduledAfterStartMinutes != 569 {
		t.Fatalf("second scheduled after start = %d", second.ScheduledAfterStartMinutes)
	}
	if !second.ScheduledAtUTC.Equal(start.Add(9*time.Hour + 29*time.Minute)) {
		t.Fatalf("second scheduled at = %s", second.ScheduledAtUTC)
	}

	last := items[len(items)-1]
	if last.AdjustMinutes != 300 {
		t.Fatalf("last adjust minutes = %d", last.AdjustMinutes)
	}
	if last.ScheduledAfterStartMinutes != 839 {
		t.Fatalf("last scheduled after start = %d", last.ScheduledAfterStartMinutes)
	}
	if !last.ScheduledAtUTC.Equal(start.Add(13*time.Hour + 59*time.Minute)) {
		t.Fatalf("last scheduled at = %s", last.ScheduledAtUTC)
	}
}

func TestBuildScheduleDoesNotGenerate330Minutes(t *testing.T) {
	items := BuildSchedule(ScheduleRequest{StartAtUTC: time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)})

	for _, item := range items {
		if item.AdjustMinutes == 330 {
			t.Fatal("generated 330 minute reminder")
		}
		if item.ScheduledAfterStartMinutes == 869 {
			t.Fatal("generated START+14h29m reminder")
		}
	}
}

func TestBuildScheduleCopiesMetadata(t *testing.T) {
	items := BuildSchedule(ScheduleRequest{
		UserID:            "user-1",
		StartPunchID:      "punch-1",
		StartPunchVersion: 7,
		LocalDate:         "2026-06-03",
		StartAtUTC:        time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC),
	})

	for _, item := range items {
		if item.UserID != "user-1" {
			t.Fatalf("UserID = %q", item.UserID)
		}
		if item.SourceStartPunchID != "punch-1" {
			t.Fatalf("SourceStartPunchID = %q", item.SourceStartPunchID)
		}
		if item.SourceStartPunchVersion != 7 {
			t.Fatalf("SourceStartPunchVersion = %d", item.SourceStartPunchVersion)
		}
		if item.LocalDate != "2026-06-03" {
			t.Fatalf("LocalDate = %q", item.LocalDate)
		}
	}
}

func TestBuildMessage(t *testing.T) {
	tests := []struct {
		name          string
		reminderType  string
		adjustMinutes int
		want          string
	}{
		{
			name:         "end reminder",
			reminderType: TypeEndReminder,
			want:         "下班提醒\n已接近标准工时\n记得下班打卡",
		},
		{
			name:          "30 minutes",
			reminderType:  TypeAdjustReminder,
			adjustMinutes: 30,
			want:          "下班提醒\n已超过标准工时 30 分钟\n记得下班打卡",
		},
		{
			name:          "60 minutes",
			reminderType:  TypeAdjustReminder,
			adjustMinutes: 60,
			want:          "下班提醒\n已超过标准工时 1 小时\n记得下班打卡",
		},
		{
			name:          "90 minutes",
			reminderType:  TypeAdjustReminder,
			adjustMinutes: 90,
			want:          "下班提醒\n已超过标准工时 1 小时 30 分钟\n记得下班打卡",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildMessage(tt.reminderType, tt.adjustMinutes)
			if got != tt.want {
				t.Fatalf("BuildMessage() = %q", got)
			}
			if lineCount := strings.Count(got, "\n") + 1; lineCount != 3 {
				t.Fatalf("line count = %d", lineCount)
			}
		})
	}
}
