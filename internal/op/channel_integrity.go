package op

import (
	"errors"
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

var ErrChannelIntegrity = errors.New("channel relationships are inconsistent; preserve the database and run db-audit before saving or refreshing")

func cachedChannelIntegrity(id int) error {
	if backupUnavailable.Load() {
		return ErrBackupCommitted
	}
	for _, grant := range channelGrantCache.GetAll() {
		m, mo := channelModelCache.Get(grant.ChannelModelID)
		k, ko := channelKeyCache.Get(grant.ChannelKeyID)
		if (mo && m.ChannelID == id || ko && k.ChannelID == id) && (!mo || !ko || m.ChannelID != k.ChannelID) {
			return fmt.Errorf("%w: channel_grants ID %d", ErrChannelIntegrity, grant.ID)
		}
	}
	return nil
}

// Read persisted IDs before ANY destructive synchronization. Inspect both ends
// of an authorization so saving either the old or new key owner is protected.
// A damaged unrelated channel does not prevent healthy-channel maintenance.
func validateChannelRelations(tx *gorm.DB, id int) error {
	if backupUnavailable.Load() {
		return ErrBackupCommitted
	}
	d, err := integrity.ReadGraph(tx)
	if err != nil {
		return err
	}
	models, keys := map[int]int{}, map[int]int{}
	for _, v := range d.ChannelModels {
		models[v.ID] = v.ChannelID
	}
	for _, v := range d.ChannelKeys {
		keys[v.ID] = v.ChannelID
	}
	grants, groups, items := map[int]bool{}, map[int]bool{}, map[int]bool{}
	for _, v := range d.ChannelGrants {
		if models[v.ChannelModelID] == id || keys[v.ChannelKeyID] == id {
			grants[v.ID] = true
		}
	}
	for _, v := range d.GroupItems {
		if grants[v.ChannelGrantID] {
			items[v.ID] = true
			groups[v.GroupID] = true
		}
	}
	for _, issue := range integrity.Inspect(d) {
		relevant := issue.Table == "channel_grants" && grants[issue.ID] || issue.Table == "groups" && groups[issue.ID] || issue.Table == "group_items" && items[issue.ID]
		if relevant {
			return fmt.Errorf("%w: %s ID %d: %s", ErrChannelIntegrity, issue.Table, issue.ID, issue.Code)
		}
	}
	return nil
}

func unavailableGroupItem(item *model.GroupItem, reason string) {
	item.Available = false
	item.UnavailableReason = fmt.Sprintf("group_items ID %d / channel_grants ID %d: %s", item.ID, item.ChannelGrantID, reason)
}
