// k6 load for MNChat: chat traffic, plus a separate search scenario.
//
// Only the chat scenario has thresholds, and k6's exit code follows them: any failed chat
// request, or a chat p95 of 300 ms or more, fails the run. Search is expected to fail while
// `make chaos-search` has it down, so its numbers are reported but never fail the run.
//
// Every message goes to #load-test with a "load-test" prefix, so test data stays out of the
// demo channels and is easy to find.
import http from 'k6/http';
import { check, sleep } from 'k6';

const API = `${__ENV.BASE_URL || 'http://api:8080'}/api/v1`;
const WORKSPACE = 1; // Acme HQ
const USERS = [1, 2, 3, 4]; // Alice, Bob, Carol, Dave
const CHANNEL = 'load-test';
const PREFIX = 'load-test';
const SEARCH_QUERIES = ['deploy', 'invoice', 'budget', 'lunch', 'rollback'];
const DURATION = __ENV.DURATION || '60s';

export const options = {
  scenarios: {
    chat: { executor: 'constant-vus', exec: 'chat', vus: Number(__ENV.VUS || 5), duration: DURATION },
    search: {
      executor: 'constant-arrival-rate',
      exec: 'search',
      rate: 2,
      timeUnit: '1s',
      duration: DURATION,
      preAllocatedVUs: 2,
      maxVUs: 4,
    },
  },
  thresholds: {
    'http_req_failed{scenario:chat}': ['rate==0'],
    'http_req_duration{scenario:chat}': ['p(95)<300'],
    // Always passes. It's here so the summary shows how search did.
    'http_req_failed{scenario:search}': ['rate>=0'],
  },
  summaryTrendStats: ['avg', 'p(95)', 'max'],
};

function headers(token) {
  const h = { 'Content-Type': 'application/json' };
  if (token) h.Authorization = `Bearer ${token}`;
  return { headers: h };
}

export function setup() {
  const tokens = USERS.map((id) => {
    const res = http.post(`${API}/auth/demo-login`, JSON.stringify({ user_id: id }), headers());
    if (res.status !== 200) throw new Error(`demo login for user ${id}: ${res.status} ${res.body}`);
    return res.json('token');
  });

  // Create #load-test on the first run, and reuse it after that.
  const auth = headers(tokens[0]);
  const created = http.post(`${API}/workspaces/${WORKSPACE}/channels`, JSON.stringify({ name: CHANNEL }), auth);
  let channelId;
  if (created.status === 201) {
    channelId = created.json('id');
  } else if (created.status === 409) {
    const list = http.get(`${API}/workspaces/${WORKSPACE}/channels`, auth);
    channelId = list.json('items').find((c) => c.name === CHANNEL).id;
  } else {
    throw new Error(`create #${CHANNEL}: ${created.status} ${created.body}`);
  }
  return { tokens, channelId };
}

// Each chat user posts, reads the channel, and replies to its own message 30% of the time.
export function chat(data) {
  const auth = headers(data.tokens[(__VU - 1) % data.tokens.length]);
  const url = `${API}/workspaces/${WORKSPACE}/channels/${data.channelId}/messages`;

  const posted = http.post(url, JSON.stringify({ body: `${PREFIX} vu${__VU} iteration ${__ITER}` }), auth);
  check(posted, { 'post is 201': (r) => r.status === 201 });

  const listed = http.get(`${url}?limit=20`, auth);
  check(listed, { 'list is 200': (r) => r.status === 200 });

  if (posted.status === 201 && Math.random() < 0.3) {
    const reply = http.post(url, JSON.stringify({ body: `${PREFIX} reply from vu${__VU}`, thread_root_id: posted.json('id') }), auth);
    check(reply, { 'reply is 201': (r) => r.status === 201 });
  }
  sleep(1);
}

export function search(data) {
  const q = SEARCH_QUERIES[__ITER % SEARCH_QUERIES.length];
  const res = http.get(`${API}/workspaces/${WORKSPACE}/search?q=${q}`, headers(data.tokens[0]));
  check(res, { 'search is 200': (r) => r.status === 200 });
}
