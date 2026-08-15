package main

import (
	"sort"
	"time"
)

// Daily reward rules (see 登录机制.md):
//
// Regular weekday login:
//   Mon +1, Tue +1, Wed +1, Thu +1, Fri +2, Sat +2, Sun +2
//
// Special date bonuses are fixed amounts (not stacked on top of the weekday
// base). When a special day is matched the total reward is the special value
// itself; on ordinary days the total is the weekday base. Multiple categories
// matching the same day add together (e.g. Lantern Festival + special week):
//   1. (Solar) Oct 1, Jan 1; (Lunar) Chinese New Year's Eve, Spring Festival -> +20
//   2. (Solar) May 1, Dec 25; (Lunar) Lantern Festival (正月十五) -> +10
//   3. Qingming (solar), Dragon Boat (lunar), Mid-Autumn (lunar) -> +5
//   4. Special week (solar): Mar 3 +20, Mar 4 +5, Mar 5 +5, Mar 6 +5, Mar 7 +5
//
// Subscription tier multiplier `Daily_Credit_Multipler` (Free=1, Plus=2,
// Pro=3, Max=4) is applied to the total in the handler that performs the
// credit transfer (see claimDaily). Keeping the multiplier outside of this
// file makes this function easy to unit-test in isolation.

// lunarSpecialDates is a year -> list of (month, day, bonus) entries that
// capture the small subset of lunar calendar events we care about. Stored as
// solar-date lookups so we don't need a full lunar converter here.
//
// Solar dates sourced from the Chinese government holiday schedule
// (国务院办公厅通知) for 2025-2030.
var lunarSpecialDates = map[int][]specialDate{
	2025: {
		{time.January, 28, 20}, // 除夕 (Lunar New Year's Eve)
		{time.January, 29, 20}, // 春节 (Lunar New Year, lunar 1/1)
		{time.February, 12, 10}, // 正月十五 (Lantern Festival)
		{time.May, 31, 5},       // 端午 (Dragon Boat, lunar 5/5)
		{time.October, 6, 5},    // 中秋 (Mid-Autumn, lunar 8/15)
	},
	2026: {
		{time.February, 16, 20}, // 除夕
		{time.February, 17, 20}, // 春节
		{time.March, 3, 10},      // 正月十五
		{time.June, 19, 5},       // 端午
		{time.September, 25, 5},  // 中秋
	},
	2027: {
		{time.February, 5, 20},  // 除夕
		{time.February, 6, 20},  // 春节
		{time.February, 20, 10}, // 正月十五
		{time.June, 9, 5},       // 端午
		{time.September, 15, 5},  // 中秋
	},
	2028: {
		{time.January, 25, 20}, // 除夕
		{time.January, 26, 20}, // 春节
		{time.February, 9, 10}, // 正月十五
		{time.May, 28, 5},      // 端午
		{time.October, 3, 5},   // 中秋
	},
	2029: {
		{time.February, 12, 20}, // 除夕
		{time.February, 13, 20}, // 春节
		{time.February, 27, 10}, // 正月十五
		{time.June, 16, 5},      // 端午
		{time.September, 22, 5}, // 中秋
	},
	2030: {
		{time.February, 2, 20},  // 除夕
		{time.February, 3, 20},  // 春节
		{time.February, 17, 10}, // 正月十五
		{time.June, 5, 5},       // 端午
		{time.September, 12, 5}, // 中秋
	},
}

type specialDate struct {
	month time.Month
	day   int
	bonus int
}

// weekdayBase: Mon-Thu +1, Fri-Sun +2.
var weekdayBase = map[time.Weekday]int{
	time.Monday:    1,
	time.Tuesday:   1,
	time.Wednesday: 1,
	time.Thursday:  1,
	time.Friday:    2,
	time.Saturday:  2,
	time.Sunday:    2,
}

// solarSpecialDates applies equally to any year (month-day pairs).
var solarSpecialDates = []specialDate{
	// +20 tier
	{time.January, 1, 20},   // 元旦
	{time.October, 1, 20},   // 国庆节
}

// solarSpecialDatesLower applies equally to any year (lower tier).
var solarSpecialDatesLower = []specialDate{
	{time.May, 1, 10},     // 劳动节
	{time.December, 25, 10}, // 圣诞节
}

// qingmingDates are solar-term dates (April 4 or 5, varies by year).
// Stored as month/day pairs; Qingming advances ~1 day every ~4 years.
var qingmingDates = map[int]specialDate{
	2025: {time.April, 4, 5},
	2026: {time.April, 5, 5},
	2027: {time.April, 5, 5},
	2028: {time.April, 4, 5},
	2029: {time.April, 4, 5},
	2030: {time.April, 5, 5},
}

// specialWeekDates captures the Mar 3-7 special week rules. Each entry is
// (month, day, bonus); the value replaces the weekday base on that day.
var specialWeekDates = []specialDate{
	{time.March, 3, 20},
	{time.March, 4, 5},
	{time.March, 5, 5},
	{time.March, 6, 5},
	{time.March, 7, 5},
}

// beijingNow returns the current time in the Beijing timezone (UTC+8), which
// is the canonical timezone used for daily reward rules and the daily reset.
func beijingNow() time.Time {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return time.Now().In(loc)
}

// DailyRewardBreakdown describes how a daily reward amount is composed so the
// frontend can show "Base + Special day + Special week = total".
type DailyRewardBreakdown struct {
	Base           int    `json:"base"`
	SpecialSolar   int    `json:"special_solar"`
	SpecialLunar   int    `json:"special_lunar"`
	SpecialWeek    int    `json:"special_week"`
	Qingming       int    `json:"qingming"`
	Total          int    `json:"total"`
	SpecialReason  string `json:"special_reason,omitempty"`
}

// CalculateDailyReward returns the credit reward for signing in on the given
// day along with a breakdown so callers can surface the reason in the UI.
func CalculateDailyReward(now time.Time) DailyRewardBreakdown {
	b := DailyRewardBreakdown{
		Base: weekdayBase[now.Weekday()],
	}

	// Solar +20 tier
	for _, d := range solarSpecialDates {
		if now.Month() == d.month && now.Day() == d.day {
			if d.bonus > b.SpecialSolar {
				b.SpecialSolar = d.bonus
			}
		}
	}

	// Solar +10 tier
	for _, d := range solarSpecialDatesLower {
		if now.Month() == d.month && now.Day() == d.day {
			if d.bonus > b.SpecialSolar {
				b.SpecialSolar = d.bonus
			}
		}
	}

	// Qingming (+5)
	if q, ok := qingmingDates[now.Year()]; ok {
		if now.Month() == q.month && now.Day() == q.day {
			b.Qingming = q.bonus
		}
	}

	// Special week (Mar 3-7)
	for _, d := range specialWeekDates {
		if now.Month() == d.month && now.Day() == d.day {
			b.SpecialWeek += d.bonus
		}
	}

	// Lunar events from the per-year lookup table.
	if entries, ok := lunarSpecialDates[now.Year()]; ok {
		for _, d := range entries {
			if now.Month() == d.month && now.Day() == d.day {
				if d.bonus > b.SpecialLunar {
					b.SpecialLunar = d.bonus
				}
			}
		}
	}

	// Special days pay a fixed amount instead of the weekday base. Multiple
	// special categories matching the same day are added together.
	special := b.SpecialSolar + b.SpecialLunar + b.SpecialWeek + b.Qingming
	if special > 0 {
		b.Total = special
	} else {
		b.Total = b.Base
	}
	if reasons := describeBreakdown(b); reasons != "" {
		b.SpecialReason = reasons
	}
	return b
}

// describeBreakdown returns a short human-readable summary of the extra
// bonuses that apply (excludes the weekday base, which is always present).
func describeBreakdown(b DailyRewardBreakdown) string {
	parts := []string{}
	if b.SpecialSolar > 0 {
		parts = append(parts, "节气")
	}
	if b.SpecialLunar > 0 {
		parts = append(parts, "传统节日")
	}
	if b.SpecialWeek > 0 {
		parts = append(parts, "特殊周登录")
	}
	if b.Qingming > 0 {
		parts = append(parts, "清明节")
	}
	sort.Strings(parts)
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "、"
		}
		out += p
	}
	return out
}
