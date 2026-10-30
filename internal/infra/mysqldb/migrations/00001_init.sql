-- +goose Up
CREATE TABLE users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    email         VARCHAR(255)    NOT NULL,
    password_hash VARCHAR(255)    NOT NULL DEFAULT '',
    display_name  VARCHAR(100)    NOT NULL,
    created_at    DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    UNIQUE KEY uq_users_email (email)
);

CREATE TABLE orgs (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name       VARCHAR(100)    NOT NULL,
    created_by BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_orgs_created_by FOREIGN KEY (created_by) REFERENCES users (id)
);

CREATE TABLE org_members (
    org_id  BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NOT NULL,
    role    ENUM('owner', 'admin', 'member') NOT NULL DEFAULT 'member',
    PRIMARY KEY (org_id, user_id),
    KEY idx_org_members_user (user_id),
    CONSTRAINT fk_org_members_org FOREIGN KEY (org_id) REFERENCES orgs (id) ON DELETE CASCADE,
    CONSTRAINT fk_org_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE workspaces (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    org_id     BIGINT UNSIGNED NOT NULL,
    name       VARCHAR(100)    NOT NULL,
    created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    KEY idx_workspaces_org (org_id),
    CONSTRAINT fk_workspaces_org FOREIGN KEY (org_id) REFERENCES orgs (id) ON DELETE CASCADE
);

CREATE TABLE workspace_members (
    workspace_id BIGINT UNSIGNED NOT NULL,
    user_id      BIGINT UNSIGNED NOT NULL,
    role         ENUM('admin', 'member') NOT NULL DEFAULT 'member',
    joined_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (workspace_id, user_id),
    KEY idx_workspace_members_user (user_id),
    CONSTRAINT fk_workspace_members_ws FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT fk_workspace_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE channels (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    workspace_id BIGINT UNSIGNED NOT NULL,
    type         ENUM('public', 'private', 'dm') NOT NULL DEFAULT 'public',
    name         VARCHAR(80)  NULL,
    dm_key       VARCHAR(255) NULL,
    created_by   BIGINT UNSIGNED NOT NULL,
    created_at   DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    archived_at  DATETIME(3)  NULL,
    UNIQUE KEY uq_channels_name (workspace_id, name),
    UNIQUE KEY uq_channels_dm (workspace_id, dm_key),
    CONSTRAINT fk_channels_ws FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT fk_channels_created_by FOREIGN KEY (created_by) REFERENCES users (id)
);

CREATE TABLE channel_members (
    channel_id       BIGINT UNSIGNED NOT NULL,
    user_id          BIGINT UNSIGNED NOT NULL,
    last_read_msg_id BIGINT UNSIGNED NULL,
    joined_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (channel_id, user_id),
    KEY idx_channel_members_user (user_id),
    CONSTRAINT fk_channel_members_channel FOREIGN KEY (channel_id) REFERENCES channels (id) ON DELETE CASCADE,
    CONSTRAINT fk_channel_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE TABLE messages (
    id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    workspace_id   BIGINT UNSIGNED NOT NULL,
    channel_id     BIGINT UNSIGNED NOT NULL,
    thread_root_id BIGINT UNSIGNED NULL,
    user_id        BIGINT UNSIGNED NOT NULL,
    body           TEXT            NOT NULL,
    reply_count    INT UNSIGNED    NOT NULL DEFAULT 0,
    created_at     DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    edited_at      DATETIME(3)     NULL,
    deleted_at     DATETIME(3)     NULL,
    KEY idx_messages_channel (channel_id, thread_root_id, id),
    FULLTEXT KEY ft_messages_body (body),
    CONSTRAINT fk_messages_ws FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE,
    CONSTRAINT fk_messages_channel FOREIGN KEY (channel_id) REFERENCES channels (id) ON DELETE CASCADE,
    CONSTRAINT fk_messages_user FOREIGN KEY (user_id) REFERENCES users (id)
);

-- +goose Down
DROP TABLE messages;
DROP TABLE channel_members;
DROP TABLE channels;
DROP TABLE workspace_members;
DROP TABLE workspaces;
DROP TABLE org_members;
DROP TABLE orgs;
DROP TABLE users;
