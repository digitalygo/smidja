package verktriggers

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	fields   [5]map[int]bool
	wildcard [5]bool
	location *time.Location
}

func ParseSchedule(expression, zone string) (Schedule, error) {
	var out Schedule
	fields := strings.Fields(expression)
	if len(fields) != 5 || zone == "" || zone == "Local" {
		return out, fmt.Errorf("schedule requires five numeric cron fields and an explicit timezone")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return out, fmt.Errorf("invalid schedule timezone")
	}
	out.location = location
	limits := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i, field := range fields {
		out.fields[i] = map[int]bool{}
		out.wildcard[i] = false
		for _, piece := range strings.Split(field, ",") {
			rangeStep := strings.Split(piece, "/")
			if rangeStep[0] == "*" {
				out.wildcard[i] = true
			}
			if len(rangeStep) > 2 {
				return Schedule{}, fmt.Errorf("invalid cron step")
			}
			step := 1
			if len(rangeStep) == 2 {
				step, err = strconv.Atoi(rangeStep[1])
				if err != nil || step < 1 || step > limits[i][1]-limits[i][0]+1 {
					return Schedule{}, fmt.Errorf("invalid cron step")
				}
			}
			from, to := limits[i][0], limits[i][1]
			if rangeStep[0] != "*" {
				bounds := strings.Split(rangeStep[0], "-")
				if len(bounds) > 2 {
					return Schedule{}, fmt.Errorf("invalid cron range")
				}
				from, err = strconv.Atoi(bounds[0])
				if err != nil {
					return Schedule{}, fmt.Errorf("invalid cron number")
				}
				to = from
				if len(bounds) == 2 {
					to, err = strconv.Atoi(bounds[1])
					if err != nil {
						return Schedule{}, fmt.Errorf("invalid cron number")
					}
				} else if len(rangeStep) == 2 {
					to = limits[i][1]
				}
			}
			if from < limits[i][0] || to > limits[i][1] || from > to {
				return Schedule{}, fmt.Errorf("cron value out of range")
			}
			for value := from; value <= to; value += step {
				if i == 4 && value == 7 {
					out.fields[i][0] = true
				} else {
					out.fields[i][value] = true
				}
			}
		}
	}
	return out, nil
}

func (s Schedule) Due(now time.Time) (string, bool) {
	if s.location == nil {
		return "", false
	}
	local := now.In(s.location)
	day, dow := s.fields[2][local.Day()], s.fields[4][int(local.Weekday())]
	dayMatch := day || dow
	if s.wildcard[2] || s.wildcard[4] {
		dayMatch = day && dow
	}
	if !s.fields[0][local.Minute()] || !s.fields[1][local.Hour()] || !s.fields[3][int(local.Month())] || !dayMatch {
		return "", false
	}
	return s.location.String() + ":" + local.Format("2006-01-02T15:04"), true
}
