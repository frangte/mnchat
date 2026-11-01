-- Demo data: long threads on clearly different topics, for summaries and search by
-- meaning. Bob leads engineering, Carol leads Support, and Dave runs finance.
-- The #leadership thread (channel 6, Alice and Bob only) proves that Carol never
-- finds a thread she can't read, however closely her query matches it.

-- +goose Up

-- #general: a failed deploy and its rollback.
INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 1, 2, 'Incident update: the website went down at 14:05, right after we deployed v3.8.0. Checkout and login are returning 502 errors. I''m leading the response, and updates will go in this thread.');
SET @root = LAST_INSERT_ID();

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 1, @root, 3, 'Support is seeing it too: 46 tickets in the last 20 minutes, mostly customers who can''t finish checkout. Is there an ETA I can give them?'),
    (1, 1, @root, 2, 'Found the cause. The release ran a database migration that adds an index to the orders table. It locked the table, every checkout request timed out, and the health checks marked all six web pods as unhealthy.'),
    (1, 1, @root, 1, 'Can we roll back instead of fixing forward? I''d rather have the site up than debug it live.'),
    (1, 1, @root, 2, 'Agreed. Rolling back to v3.7.2 now. Nothing in v3.7.2 needs the new index.'),
    (1, 1, @root, 2, 'Rollback finished at 14:41. Checkout and login work again and the error rate is back under 0.1%. Total downtime was 36 minutes.'),
    (1, 1, @root, 4, 'From the payments side, 212 checkout attempts failed during the outage. None of the cards were charged, so there is nothing to refund.'),
    (1, 1, @root, 3, 'Thanks. I''ve updated the status page, and I''m emailing every customer who opened a ticket with an apology and a 10% discount code.'),
    (1, 1, @root, 2, 'Postmortem is Thursday at 10:00. Two action items so far: run schema migrations as a separate step before the deploy, and send each release to one canary pod first, so a bad build only reaches 5% of traffic.');

UPDATE messages SET reply_count = 8 WHERE id = @root;

-- #general: a late vendor payment.
INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 1, 4, 'Heads up: Northwind Logistics says invoice INV-2291 for $18,400, our September warehousing, is 21 days overdue. They will pause our outbound shipments on Monday unless it is paid.');
SET @root = LAST_INSERT_ID();

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 1, @root, 1, 'How did this slip? Northwind has always been on 30-day terms.'),
    (1, 1, @root, 4, 'The invoice went to the old billing@acme.test mailbox, which we retired in August, so it never reached accounts payable. I only found out when their collections team called this morning.'),
    (1, 1, @root, 3, 'A pause would hit the 140 orders already waiting in their warehouse. Several are for the Hartwell account, which has a delivery deadline next Wednesday.'),
    (1, 1, @root, 1, 'Pay it today by wire, and pay the late fee as well. It isn''t worth arguing over.'),
    (1, 1, @root, 4, 'Wire sent at 15:20: $18,676 in total, including the 1.5% late fee of $276. I''ve emailed Northwind the confirmation.'),
    (1, 1, @root, 3, 'I called our account manager at Northwind. Shipments continue as normal, and they now send invoices to ap@acme.test.'),
    (1, 1, @root, 4, 'So this can''t happen again, the old billing@ address now forwards to accounts payable, and I get a weekly list of vendor invoices older than 25 days.');

UPDATE messages SET reply_count = 7 WHERE id = @root;

-- #general: a budget overrun.
INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 1, 4, 'The October cloud bill came in at $61,300 against a budget of $42,000. That is 46% over, and the third month in a row that we have gone over.');
SET @root = LAST_INSERT_ID();

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 1, @root, 2, 'I looked into it. Three things: the staging cluster stayed at full production size for two weeks after the load test, log retention went from 30 to 90 days, and the GPU node for the AI experiment was never shut down.'),
    (1, 1, @root, 1, 'What does each of those cost per month?'),
    (1, 1, @root, 2, 'Roughly $9,500 for the oversized staging cluster, $4,200 for the longer log retention, and $5,600 for the GPU node.'),
    (1, 1, @root, 4, 'If we fix all three, November should land at about $42,000, right on budget. The quarter is still $19,300 over because of October.'),
    (1, 1, @root, 1, 'Do all three: scale staging down and turn it off at night, go back to 30 days of logs, and shut the GPU node down until the experiment has its own budget. Dave, please cover the $19,300 from the unused travel budget.'),
    (1, 1, @root, 3, 'Will turning staging off at night take down the Hartwell demo environment? Their team logs in from Singapore during our evening.'),
    (1, 1, @root, 2, 'Good catch. The Hartwell demo runs in its own small environment, so it stays up. Only the shared staging cluster turns off at night.'),
    (1, 1, @root, 4, 'Done. The travel budget has moved, and there is now a billing alert at 80% of the monthly budget, so we hear about an overrun in week two instead of from the invoice.');

UPDATE messages SET reply_count = 8 WHERE id = @root;

-- #random: a team lunch.
INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 2, 3, 'Team lunch on Friday, November 13! The options are Sakura Ramen, Bella Napoli for pizza, or Bangkok Garden for Thai. Reply with your vote by Wednesday.');
SET @root = LAST_INSERT_ID();

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 2, @root, 1, 'Sakura Ramen for me. The company pays, up to $25 per person.'),
    (1, 2, @root, 2, 'Ramen too. Can we start at 12:30? Our sprint review runs until 12:15.'),
    (1, 2, @root, 4, 'I''m vegetarian. Does Sakura have something for me? If not, I vote for Bangkok Garden.'),
    (1, 2, @root, 3, 'I checked, and Sakura has a vegetable miso ramen and tofu rice bowls. So it''s Sakura Ramen, four votes to none.'),
    (1, 2, @root, 3, 'Booked a table for 9 at Sakura Ramen on Friday, November 13 at 12:30, under the name Acme. It''s a 10-minute walk, so we leave the office at 12:20.'),
    (1, 2, @root, 4, 'I''ll pay with the company card and file the receipt in the expense system.'),
    (1, 2, @root, 1, 'Let''s invite the two new contractors on the Hartwell project as well. Carol, can you change the booking to 11?'),
    (1, 2, @root, 3, 'Done. The table is now for 11 people, same time.');

UPDATE messages SET reply_count = 8 WHERE id = @root;

-- #leadership: a confidential restructuring that Carol must never find.
INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 6, 1, 'Confidential: before the board meeting on Thursday, November 12, I want us to agree on restructuring Support. The draft plan is in the board folder.');
SET @root = LAST_INSERT_ID();

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 6, @root, 2, 'I read it. Merging Support into Customer Success makes sense. Most escalations end up with my engineers anyway, and two separate queues slow everyone down.'),
    (1, 6, @root, 1, 'The plan: Carol''s Support team and the Customer Success team become one team under a single manager, and we pause hiring for the two open Support roles until Q1.'),
    (1, 6, @root, 2, 'That takes the two teams from 14 planned people to 12. Who manages the merged team?'),
    (1, 6, @root, 1, 'I want to offer it to Carol. She knows the customers and already runs the bigger of the two teams. Nobody is being let go.'),
    (1, 6, @root, 2, 'Then we should tell her before the announcement, not after. Otherwise she will hear rumors first.'),
    (1, 6, @root, 1, 'Agreed. I''ll talk to Carol on November 13, the day after the board approves it, and we announce it to the company on November 16. Until then, keep this in this channel.'),
    (1, 6, @root, 2, 'One risk: the Hartwell rollout needs support coverage in December, and with the hiring pause we are short for about six weeks.'),
    (1, 6, @root, 1, 'Good point. We keep one contract support engineer through December, with a budget of $12,000. I''ll add it to the board pack.');

UPDATE messages SET reply_count = 8 WHERE id = @root;

-- +goose Down
-- Deleting a root also deletes its summary, through the thread_summaries foreign key.
SET @deploy = (SELECT id FROM messages WHERE channel_id = 1 AND thread_root_id IS NULL AND body LIKE 'Incident update: the website went down%' ORDER BY id LIMIT 1);
SET @vendor = (SELECT id FROM messages WHERE channel_id = 1 AND thread_root_id IS NULL AND body LIKE 'Heads up: Northwind Logistics%' ORDER BY id LIMIT 1);
SET @budget = (SELECT id FROM messages WHERE channel_id = 1 AND thread_root_id IS NULL AND body LIKE 'The October cloud bill%' ORDER BY id LIMIT 1);
SET @lunch = (SELECT id FROM messages WHERE channel_id = 2 AND thread_root_id IS NULL AND body LIKE 'Team lunch on Friday, November 13!%' ORDER BY id LIMIT 1);
SET @restructure = (SELECT id FROM messages WHERE channel_id = 6 AND thread_root_id IS NULL AND body LIKE 'Confidential: before the board meeting%' ORDER BY id LIMIT 1);

DELETE FROM messages WHERE thread_root_id IN (@deploy, @vendor, @budget, @lunch, @restructure);
DELETE FROM messages WHERE id IN (@deploy, @vendor, @budget, @lunch, @restructure);
