CREATE TABLE IF NOT EXISTS console_fact_journals(
 source_key TEXT PRIMARY KEY,generation TEXT NOT NULL,head BIGINT NOT NULL DEFAULT 0,error_class TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS console_fact_journal_objects(
 source_key TEXT NOT NULL REFERENCES console_fact_journals(source_key),seq BIGINT NOT NULL,
 object_key TEXT NOT NULL,etag TEXT NOT NULL,bytes BIGINT NOT NULL,modified_ms BIGINT NOT NULL,
 PRIMARY KEY(source_key,seq),UNIQUE(source_key,object_key));
