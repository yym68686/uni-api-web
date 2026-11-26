-- Tier checks describe the account, group and upstream model. A replacement
-- test key for the same group does not invalidate those observations.
-- Changing the group's platform still invalidates its old protocol evidence;
-- changing the account's site address already deletes its old model rows.
DROP TRIGGER console_clear_site_tier_evidence_v1 ON console_sub_targets;
CREATE TRIGGER console_clear_site_tier_evidence_v1
 AFTER UPDATE OF platform ON console_sub_targets
 FOR EACH ROW WHEN (OLD.platform IS DISTINCT FROM NEW.platform)
 EXECUTE FUNCTION console_clear_site_tier_evidence_v1();
