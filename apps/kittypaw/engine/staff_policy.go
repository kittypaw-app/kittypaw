package engine

import (
	"context"
	"strings"
)

type staffPolicyContextKey struct{}

type staffPolicy struct {
	StaffID       string
	AllowedSkills map[string]struct{}
}

func ContextWithStaffPolicy(ctx context.Context, staffID string, allowedSkills []string) context.Context {
	if len(allowedSkills) == 0 {
		return ctx
	}
	policy := staffPolicy{
		StaffID:       strings.TrimSpace(staffID),
		AllowedSkills: make(map[string]struct{}, len(allowedSkills)),
	}
	for _, skill := range allowedSkills {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			continue
		}
		policy.AllowedSkills[strings.ToLower(skill)] = struct{}{}
	}
	if len(policy.AllowedSkills) == 0 {
		return ctx
	}
	return context.WithValue(ctx, staffPolicyContextKey{}, policy)
}

func staffPolicyFromContext(ctx context.Context) (staffPolicy, bool) {
	if ctx == nil {
		return staffPolicy{}, false
	}
	policy, ok := ctx.Value(staffPolicyContextKey{}).(staffPolicy)
	return policy, ok && len(policy.AllowedSkills) > 0
}

func staffAllowsSkill(ctx context.Context, skillName string) (string, bool) {
	policy, ok := staffPolicyFromContext(ctx)
	if !ok {
		return "", true
	}
	_, allowed := policy.AllowedSkills[strings.ToLower(strings.TrimSpace(skillName))]
	return policy.StaffID, allowed
}

func skillAllowedByList(allowedSkills []string, skillName string) bool {
	if len(allowedSkills) == 0 {
		return true
	}
	skillName = strings.ToLower(strings.TrimSpace(skillName))
	for _, allowed := range allowedSkills {
		if strings.ToLower(strings.TrimSpace(allowed)) == skillName {
			return true
		}
	}
	return false
}
