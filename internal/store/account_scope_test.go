package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestAccountScopeZaiHistoryCyclesAndHourlyUsage(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, b := s.ForAccount(101), s.ForAccount(102)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		st    *Store
		value int64
	}{{s, 3}, {a, 11}, {b, 99}} {
		if _, err := item.st.InsertZaiSnapshot(&api.ZaiSnapshot{CapturedAt: now, TokensCurrentValue: float64(item.value), TokensUsage: 100}); err != nil {
			t.Fatal(err)
		}
		if _, err := item.st.CreateZaiCycle("tokens", now.Add(-time.Hour), nil); err != nil {
			t.Fatal(err)
		}
		if err := item.st.UpdateZaiCycle("tokens", item.value, item.value); err != nil {
			t.Fatal(err)
		}
		if err := item.st.InsertZaiHourlyUsage("2026-11-23 12:00", item.value, item.value, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.CloseZaiCycle("tokens", now, 99, 99); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		st    *Store
		value float64
	}{{s, 3}, {a, 11}, {b, 99}} {
		latest, err := item.st.QueryLatestZai()
		if err != nil || latest == nil || latest.TokensCurrentValue != item.value {
			t.Fatalf("latest value: %+v, %v; want %v", latest, err, item.value)
		}
		for _, sampled := range []bool{false, true} {
			var snapshots []*api.ZaiSnapshot
			if sampled {
				snapshots, err = item.st.QueryZaiRangeSampled(now.Add(-time.Minute), now.Add(time.Minute))
			} else {
				snapshots, err = item.st.QueryZaiRange(now.Add(-time.Minute), now.Add(time.Minute), 1)
			}
			if err != nil || len(snapshots) != 1 || snapshots[0].TokensCurrentValue != item.value {
				t.Fatalf("history sampled=%v: %+v, %v", sampled, snapshots, err)
			}
		}
		hourly, err := item.st.QueryZaiHourlyUsage(now.Add(-time.Hour), now.Add(time.Hour))
		if err != nil || len(hourly) != 1 || hourly[0].TokensUsed == nil || *hourly[0].TokensUsed != int64(item.value) {
			t.Fatalf("hourly: %+v, %v", hourly, err)
		}
	}
	cycle, err := a.QueryActiveZaiCycle("tokens")
	if err != nil || cycle == nil || cycle.PeakValue != 11 {
		t.Fatalf("other account closed or changed cycle: %+v, %v", cycle, err)
	}
	active, err := b.QueryActiveZaiCycle("tokens")
	if err != nil || active != nil {
		t.Fatalf("closed cycle remains active: %+v, %v", active, err)
	}
}

func TestAccountScopeSessionsAndNotifications(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, b := s.ForAccount(101), s.ForAccount(102)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	if err := a.CreateSession("a", now, 60, "zai"); err != nil {
		t.Fatal(err)
	}
	if err := b.CreateSession("b", now, 60, "zai"); err != nil {
		t.Fatal(err)
	}
	rows, err := a.QuerySessionHistory("zai")
	if err != nil || len(rows) != 1 || rows[0].ID != "a" {
		t.Fatalf("sessions leaked: %+v, %v", rows, err)
	}
	if err := a.UpsertNotificationLogAt("zai", "tokens", "threshold", 80, now); err != nil {
		t.Fatal(err)
	}
	if err := b.UpsertNotificationLogAt("zai", "tokens", "threshold", 90, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := b.ClearNotificationLog("zai", "tokens"); err != nil {
		t.Fatal(err)
	}
	got, utilization, err := a.GetLastNotification("zai", "tokens", "threshold")
	if err != nil || !got.Equal(now) || utilization != 80 {
		t.Fatalf("notification overwritten or cleared by another account: %v %v %v", got, utilization, err)
	}
}
