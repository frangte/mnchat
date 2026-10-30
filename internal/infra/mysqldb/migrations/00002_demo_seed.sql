-- Demo data: two orgs (tenants). Carol belongs to both, to show workspace switching.

-- +goose Up
INSERT INTO users (id, email, display_name) VALUES
    (1, 'alice@acme.test', 'Alice Chen'),
    (2, 'bob@acme.test', 'Bob Martin'),
    (3, 'carol@acme.test', 'Carol Diaz'),
    (4, 'dave@acme.test', 'Dave Kim'),
    (5, 'erin@globex.test', 'Erin Walsh');

INSERT INTO orgs (id, name, created_by) VALUES
    (1, 'Acme Inc.', 1),
    (2, 'Globex Corp', 5);

INSERT INTO org_members (org_id, user_id, role) VALUES
    (1, 1, 'owner'), (1, 2, 'admin'), (1, 3, 'member'), (1, 4, 'member'),
    (2, 5, 'owner'), (2, 3, 'member');

INSERT INTO workspaces (id, org_id, name) VALUES
    (1, 1, 'Acme HQ'),
    (2, 2, 'Globex Labs');

INSERT INTO workspace_members (workspace_id, user_id, role) VALUES
    (1, 1, 'admin'), (1, 2, 'admin'), (1, 3, 'member'), (1, 4, 'member'),
    (2, 5, 'admin'), (2, 3, 'member');

INSERT INTO channels (id, workspace_id, type, name, created_by) VALUES
    (1, 1, 'public', 'general', 1),
    (2, 1, 'public', 'random', 1),
    (3, 1, 'public', 'engineering', 2),
    (4, 2, 'public', 'general', 5),
    (5, 2, 'public', 'research', 5);

INSERT INTO channel_members (channel_id, user_id) VALUES
    (1, 1), (1, 2), (1, 3), (1, 4),
    (2, 1), (2, 2), (2, 3), (2, 4),
    (3, 1), (3, 2),
    (4, 5), (4, 3),
    (5, 5);

INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 1, 1, 'Welcome to Acme HQ! 👋 This is the place for company-wide updates.'),
    (1, 1, 2, 'Thanks Alice! Excited to try MNChat.'),
    (1, 1, 3, 'Hi everyone 🙌'),
    (1, 3, 2, 'Reminder: release planning tomorrow at 10:00.'),
    (2, 4, 5, 'Welcome to Globex Labs. Acme folks cannot see anything in here.');

-- +goose Down
DELETE FROM messages WHERE workspace_id IN (1, 2);
DELETE FROM channel_members WHERE channel_id IN (1, 2, 3, 4, 5);
DELETE FROM channels WHERE workspace_id IN (1, 2);
DELETE FROM workspace_members WHERE workspace_id IN (1, 2);
DELETE FROM workspaces WHERE id IN (1, 2);
DELETE FROM org_members WHERE org_id IN (1, 2);
DELETE FROM orgs WHERE id IN (1, 2);
DELETE FROM users WHERE id IN (1, 2, 3, 4, 5);
