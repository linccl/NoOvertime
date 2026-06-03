package reminders

import (
	"strconv"
	"time"
)

const (
	TypeEndReminder    = "END_REMINDER"
	TypeAdjustReminder = "ADJUST_REMINDER"

	endReminderAfterStartMinutes  = 539
	adjustReminderIntervalMinutes = 30
	maxAdjustReminderMinutes      = 300
	standardWorkdayMinutes        = 540
)

type ScheduleRequest struct {
	StartPunchID      string
	StartPunchVersion int64
	UserID            string
	LocalDate         string
	StartAtUTC        time.Time
}

type ScheduleItem struct {
	UserID                     string
	SourceStartPunchID         string
	SourceStartPunchVersion    int64
	LocalDate                  string
	ReminderType               string
	AdjustMinutes              int
	ScheduledAfterStartMinutes int
	ScheduledAtUTC             time.Time
	Message                    string
}

func BuildSchedule(req ScheduleRequest) []ScheduleItem {
	items := make([]ScheduleItem, 0, 11)
	items = append(items, buildItem(req, TypeEndReminder, 0, endReminderAfterStartMinutes))
	for adjustMinutes := adjustReminderIntervalMinutes; adjustMinutes <= maxAdjustReminderMinutes; adjustMinutes += adjustReminderIntervalMinutes {
		afterStart := standardWorkdayMinutes + adjustMinutes - 1
		items = append(items, buildItem(req, TypeAdjustReminder, adjustMinutes, afterStart))
	}
	return items
}

func buildItem(req ScheduleRequest, reminderType string, adjustMinutes, afterStartMinutes int) ScheduleItem {
	return ScheduleItem{
		UserID:                     req.UserID,
		SourceStartPunchID:         req.StartPunchID,
		SourceStartPunchVersion:    req.StartPunchVersion,
		LocalDate:                  req.LocalDate,
		ReminderType:               reminderType,
		AdjustMinutes:              adjustMinutes,
		ScheduledAfterStartMinutes: afterStartMinutes,
		ScheduledAtUTC:             req.StartAtUTC.Add(time.Duration(afterStartMinutes) * time.Minute),
		Message:                    BuildMessage(reminderType, adjustMinutes),
	}
}

func BuildMessage(reminderType string, adjustMinutes int) string {
	if reminderType == TypeAdjustReminder && adjustMinutes > 0 {
		return "下班提醒\n已超过标准工时 " + formatAdjustMinutes(adjustMinutes) + "\n记得下班打卡"
	}
	return "下班提醒\n已接近标准工时\n记得下班打卡"
}

func formatAdjustMinutes(minutes int) string {
	hours := minutes / 60
	remaining := minutes % 60
	switch {
	case hours == 0:
		return strconv.Itoa(remaining) + " 分钟"
	case remaining == 0:
		return strconv.Itoa(hours) + " 小时"
	default:
		return strconv.Itoa(hours) + " 小时 " + strconv.Itoa(remaining) + " 分钟"
	}
}
