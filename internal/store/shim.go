package store

import (
	"context"
	"fmt"

	"github.com/khanakia/repolink-go/internal/ent"
	"github.com/khanakia/repolink-go/internal/ent/runlog"
)

// toProfile converts an ent row to the domain Profile type.
func toProfile(r *ent.Profile) Profile {
	return Profile{
		ID:        r.ID,
		Name:      r.Name,
		Hostname:  r.Hostname,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

func toMapping(r *ent.RepoMapping) Mapping {
	return Mapping{
		ID:        r.ID,
		SourceRel: r.SourceRel,
		RepoURL:   r.RepoURL,
		TargetRel: r.TargetRel,
		LinkName:  r.LinkName,
		Kind:      string(r.Kind),
		State:     string(r.State),
		Notes:     r.Notes,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// LogRun implementation lives here to keep ent_store.go focused on the
// most-churned CRUD. It maps op/result strings to the generated enums.
func (s *entStore) LogRun(ctx context.Context, in NewRun) error {
	c := s.client.RunLog.Create().
		SetProfileID(in.ProfileID).
		SetOp(runlog.Op(in.Op))
	if in.Result != "" {
		c = c.SetResult(runlog.Result(in.Result))
	}
	if in.MappingID != "" {
		c = c.SetMappingID(in.MappingID)
	}
	if in.UserEmail != "" {
		c = c.SetUserEmail(in.UserEmail)
	}
	if in.UserName != "" {
		c = c.SetUserName(in.UserName)
	}
	if in.Message != "" {
		c = c.SetMessage(in.Message)
	}
	if _, err := c.Save(ctx); err != nil {
		return fmt.Errorf("insert run_log: %w", err)
	}
	return nil
}
