package backups

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type DailySchedule struct {
	hour     int
	minute   int
	location *time.Location
}

func ParseDailySchedule(value, timezone string) (DailySchedule, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return DailySchedule{}, errors.New("backup schedule must use HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	location, locationErr := time.LoadLocation(timezone)
	if hourErr != nil || minuteErr != nil || locationErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return DailySchedule{}, errors.New("backup schedule or timezone is invalid")
	}
	return DailySchedule{hour: hour, minute: minute, location: location}, nil
}

func (schedule DailySchedule) Next(after time.Time) time.Time {
	local := after.In(schedule.location)
	next := time.Date(local.Year(), local.Month(), local.Day(), schedule.hour, schedule.minute, 0, 0, schedule.location)
	if !next.After(local) {
		next = time.Date(local.Year(), local.Month(), local.Day()+1, schedule.hour, schedule.minute, 0, 0, schedule.location)
	}
	return next.UTC()
}

func (schedule DailySchedule) String() string {
	return fmt.Sprintf("%02d:%02d", schedule.hour, schedule.minute)
}

func (schedule DailySchedule) Timezone() string { return schedule.location.String() }
