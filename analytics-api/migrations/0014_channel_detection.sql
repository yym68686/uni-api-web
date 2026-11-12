-- Console-only opt-out from batch probes. Missing settings mean enabled.
CREATE TABLE console_channel_detection_settings (
 owner text NOT NULL,
 scope text NOT NULL,
 source_id text NOT NULL DEFAULT '',
 provider text NOT NULL DEFAULT '',
 account_id text NOT NULL DEFAULT '',
 group_id bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(owner,scope),
 CHECK ((source_id<>'' AND provider<>'' AND account_id='' AND group_id=0)
     OR (source_id='' AND provider='' AND account_id<>'' AND group_id>0))
);
CREATE INDEX console_channel_detection_group ON console_channel_detection_settings(account_id,group_id,owner) WHERE account_id<>'';
