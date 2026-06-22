package model

import (
	"testing"
	"time"
)

func TestRoleHelpers(t *testing.T) {
	roles := NormalizeRoles([]UserRole{UserRoleVIP, UserRoleVIP, "bad", UserRoleKnowledgeStaff})
	if len(roles) != 2 || roles[0] != UserRoleVIP || roles[1] != UserRoleKnowledgeStaff {
		t.Fatalf("NormalizeRoles = %v", roles)
	}
	if got := NormalizeRoles(nil); len(got) != 1 || got[0] != UserRoleNormal {
		t.Fatalf("empty roles should default to normal, got %v", got)
	}
	if PrimaryRole([]UserRole{UserRoleVIP, UserRoleAdmin, UserRoleKnowledgeStaff}) != UserRoleAdmin {
		t.Fatal("admin should be preferred primary role")
	}
	for _, role := range []UserRole{UserRoleNormal, UserRoleVIP, UserRoleKnowledgeStaff, UserRoleKnowledgeExpert, UserRoleAdmin} {
		if !IsValidUserRole(role) {
			t.Fatalf("valid role rejected: %s", role)
		}
	}
	if IsValidUserRole("owner") {
		t.Fatal("unknown role should be invalid")
	}
}

func TestUserRoleAndConcurrency(t *testing.T) {
	user := &User{Role: UserRoleNormal, Roles: []UserRole{UserRoleVIP, UserRoleKnowledgeStaff}}
	if !user.HasRole(UserRoleNormal) || !user.HasRole(UserRoleVIP) || !user.HasRole(UserRoleKnowledgeStaff) {
		t.Fatalf("HasRole failed for user %+v", user)
	}
	if user.HasRole(UserRoleAdmin) {
		t.Fatal("user should not have admin role")
	}
	if (&User{Roles: []UserRole{UserRoleAdmin}}).MaxConcurrentSessions() != 10 {
		t.Fatal("admin default max sessions should be 10")
	}
	if (&User{Roles: []UserRole{UserRoleVIP}}).MaxConcurrentSessions() != 2 {
		t.Fatal("vip default max sessions should be 2")
	}
	if (&User{MaxSessions: 7, Roles: []UserRole{UserRoleNormal}}).MaxConcurrentSessions() != 7 {
		t.Fatal("explicit max sessions should win")
	}
}

func TestKnowledgePublishTargets(t *testing.T) {
	targets := NormalizeKnowledgePublishTargets([]KnowledgePublishTarget{
		KnowledgePublishSkill,
		KnowledgePublishSkill,
		"bad",
		KnowledgePublishLocal,
	})
	if len(targets) != 2 || targets[0] != KnowledgePublishSkill || targets[1] != KnowledgePublishLocal {
		t.Fatalf("NormalizeKnowledgePublishTargets = %v", targets)
	}
	if got := NormalizeKnowledgePublishTargets(nil); len(got) != 1 || got[0] != KnowledgePublishLocal {
		t.Fatalf("empty publish targets should default to local, got %v", got)
	}
}

func TestSessionDurationSeconds(t *testing.T) {
	start := testTime(10)
	closed := testTime(75)
	if got := (&Session{StartedAt: &start, ClosedAt: &closed}).DurationSeconds(testTime(100)); got != 65 {
		t.Fatalf("closed duration = %d", got)
	}
	if got := (&Session{StartedAt: &start}).DurationSeconds(testTime(40)); got != 30 {
		t.Fatalf("active duration = %d", got)
	}
	if got := (&Session{}).DurationSeconds(testTime(40)); got != 0 {
		t.Fatalf("missing startedAt duration = %d", got)
	}
}

func testTime(seconds int64) time.Time {
	return time.Unix(seconds, 0)
}
