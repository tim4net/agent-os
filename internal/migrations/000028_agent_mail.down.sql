-- Down 028: drop agent-to-agent messaging. Safe to reverse: the table owns no
-- outbound FKs that other tables reference, so dropping it cannot orphan rows.
DROP TABLE IF EXISTS agent_mail;
DROP TYPE IF EXISTS mail_status;
DROP TYPE IF EXISTS mail_priority;
