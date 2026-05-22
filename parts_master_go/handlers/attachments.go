package handlers

import (
	"context"
	"fmt"
)

// softDeleteAttachment sets is_active=0 on an attachment row.
// ownerCol/ownerID add an ownership filter when non-empty/non-zero.
func (h *Handler) softDeleteAttachment(ctx context.Context, table, idCol string, id int, ownerCol string, ownerID int) error {
	if ownerCol != "" {
		_, err := h.execContext(ctx, fmt.Sprintf(
			`UPDATE %s SET is_active=0 WHERE %s=@p1 AND %s=@p2`, table, idCol, ownerCol,
		), id, ownerID)
		return err
	}
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET is_active=0 WHERE %s=@p1`, table, idCol,
	), id)
	return err
}

// setPrimaryAttachment updates a parent record's primary attachment pointer.
// Pass nil for attachmentID to clear the primary.
func (h *Handler) setPrimaryAttachment(ctx context.Context, table, idCol, primaryCol string, parentID int, attachmentID any) error {
	_, err := h.execContext(ctx, fmt.Sprintf(
		`UPDATE %s SET %s=@p1 WHERE %s=@p2`, table, primaryCol, idCol,
	), attachmentID, parentID)
	return err
}
