-- Shared per-group epoch writes from nested projection triggers can deadlock
-- against source revision writes. Keep the cache, but require the existing
-- snapshot-wide proof. A fresh reset fences all old local proofs, including
-- those read by a previous app version during a rolling deploy.
CREATE OR REPLACE FUNCTION console_management_display_epoch_v1() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
 RETURN NULL;
END $$;

INSERT INTO console_management_display_epochs(kind,account_id,group_id,writer)
VALUES('reset','',0,pg_current_xact_id())
ON CONFLICT(kind,account_id,group_id) DO UPDATE SET writer=EXCLUDED.writer;

-- Old writers may remain live. Their version is from the same pinned source
-- snapshot as the payload, so a global-proof reader can safely use that version.
CREATE FUNCTION console_management_display_global_proof_v1() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
 NEW.local_version:='';
 RETURN NEW;
END $$;

CREATE TRIGGER console_management_display_global_proof
BEFORE INSERT OR UPDATE ON console_management_display_groups
FOR EACH ROW EXECUTE FUNCTION console_management_display_global_proof_v1();
