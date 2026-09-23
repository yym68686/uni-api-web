package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *controlStore) checkControlInstance(ctx context.Context, src controlSource, instance string) error {
	var retired bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM console_control_retired_instances WHERE source_id=$1 AND target_hash=$2 AND instance_id=$3)`, src.ID, controlTarget(src), instance).Scan(&retired)
	if err != nil {
		return err
	}
	if retired {
		return errors.New("请求仍到达已被替换的旧实例，已保存配置保持不变；请等待切流完成后刷新")
	}
	return nil
}

// The source lock serializes adoption with UI writes. Retire the old identity
// in the same transaction, so a delayed connection cannot adopt it again and
// capture its stale state over the latest saved intent.
func (s *controlStore) adoptControlInstance(ctx context.Context, src controlSource, old retainedRecord, live retainedLive) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if old.Instance != "" && old.Instance != live.Instance && old.Target == controlTarget(src) {
		_, err = tx.ExecContext(ctx, `INSERT INTO console_control_retired_instances(source_id,target_hash,instance_id,superseded_by) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, src.ID, old.Target, old.Instance, live.Instance)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE console_control_snapshots SET instance_id=$2,revision=$3,restoring_instance='',restore_revision='',status='saved',message='',restored_at=now(),checked_at=now() WHERE source_id=$1`, src.ID, live.Instance, live.Revision)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// A receipt alone is insufficient: require the exact issued snapshot, an
// unchanged runtime revision (including base configuration), and full content
// equivalence. Unknown, edited, or foreign bootstraps remain protected.
func (s *controlStore) verifyBootstrap(ctx context.Context, src controlSource, live retainedLive) error {
	fail := func(reason string) error {
		return fmt.Errorf("新实例配置与已保存版本不同，自动恢复已暂停：%s（当前实例 %s，版本 %s）。未覆盖任何配置", reason, live.Instance, live.Revision)
	}
	b := live.Bootstrap
	if b == nil || b.SnapshotID == "" {
		return fail("缺少可验证的启动恢复凭据")
	}
	if !b.Unchanged || b.AppliedRevision != live.Revision {
		return fail("启动恢复后已发生独立修改")
	}
	var record retainedRecord
	err := s.db.QueryRowContext(ctx, `SELECT encrypted_snapshot FROM console_control_bootstraps WHERE source_id=$1 AND target_hash=$2 AND snapshot_id=$3`, src.ID, controlTarget(src), b.SnapshotID).Scan(&record.Encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return fail("启动快照不属于当前来源或已不可验证")
	}
	if err != nil {
		return err
	}
	snapshot, err := s.retainedSnapshot(record)
	if err != nil {
		return err
	}
	if !controlEquivalent(live, snapshot) {
		return fail("运行配置与启动快照不一致")
	}
	return nil
}
