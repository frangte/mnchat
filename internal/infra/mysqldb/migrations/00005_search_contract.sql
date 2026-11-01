-- The contract between chat and search. Search reads only these views, on the
-- replica, so chat can change its tables as long as the views keep their shape.
-- A breaking change means a new _v2 view, not an edit to a _v1 view.

-- +goose Up
CREATE TABLE thread_summaries (
    thread_root_id BIGINT UNSIGNED NOT NULL PRIMARY KEY,
    workspace_id   BIGINT UNSIGNED NOT NULL,
    channel_id     BIGINT UNSIGNED NOT NULL,
    last_msg_id    BIGINT UNSIGNED NOT NULL,
    summary        TEXT            NOT NULL,
    model          VARCHAR(100)    NOT NULL,
    embedding      VECTOR(1024)    NOT NULL,
    created_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    KEY idx_thread_summaries_channel (workspace_id, channel_id),
    CONSTRAINT fk_thread_summaries_root FOREIGN KEY (thread_root_id) REFERENCES messages (id) ON DELETE CASCADE
);

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_messages_v1 AS
    SELECT id, workspace_id, channel_id, thread_root_id, body
    FROM messages
    WHERE deleted_at IS NULL;

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_thread_messages_v1 AS
    SELECT m.id, m.workspace_id, m.channel_id, m.thread_root_id,
           u.display_name AS user_name, m.body, m.created_at
    FROM messages m
    JOIN users u ON u.id = m.user_id
    WHERE m.deleted_at IS NULL;

CREATE ALGORITHM=MERGE SQL SECURITY DEFINER VIEW search_threads_v1 AS
    SELECT thread_root_id, workspace_id, channel_id, last_msg_id, embedding
    FROM thread_summaries;

GRANT SELECT ON mnchat.search_messages_v1 TO 'search_reader'@'%';
GRANT SELECT ON mnchat.search_thread_messages_v1 TO 'search_reader'@'%';
GRANT SELECT ON mnchat.search_threads_v1 TO 'search_reader'@'%';

-- +goose Down
REVOKE SELECT ON mnchat.search_threads_v1 FROM 'search_reader'@'%';
REVOKE SELECT ON mnchat.search_thread_messages_v1 FROM 'search_reader'@'%';
REVOKE SELECT ON mnchat.search_messages_v1 FROM 'search_reader'@'%';

DROP VIEW search_threads_v1;
DROP VIEW search_thread_messages_v1;
DROP VIEW search_messages_v1;
DROP TABLE thread_summaries;
