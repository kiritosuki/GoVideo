import { check, sleep } from 'k6';
import { ensureAccount, ensureVideo, postJSON, sleepSeconds } from './helpers.js';

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-vus',
      vus: Number(__ENV.VUS || 5),
      duration: __ENV.DURATION || '30s',
    },
  },
  thresholds: {
    'http_req_failed{api:comment_publish}': ['rate<0.05'],
    'http_req_duration{api:comment_publish}': ['p(95)<800', 'p(99)<1500'],
  },
};

export function setup() {
  const users = Number(__ENV.COMMENT_USERS || 5);
  const accounts = [];

  // comment/publish 有账号维度限流，所以默认准备多个账号轮流发评论。
  for (let i = 0; i < users; i += 1) {
    accounts.push(ensureAccount(i));
  }

  const videoID = ensureVideo(accounts[0].token);
  return {
    videoID,
    tokens: accounts.map((account) => account.token),
  };
}

export default function (data) {
  const token = data.tokens[(__VU - 1) % data.tokens.length];
  const res = postJSON('/comment/publish', {
    video_id: data.videoID,
    content: `perf comment vu=${__VU} iter=${__ITER}`,
  }, token, { api: 'comment_publish' });

  // 429 表示账号限流生效；真正测写入吞吐时应增大 COMMENT_USERS 或调低 VUS。
  check(res, {
    'comment publish status is 200': (r) => r.status === 200,
  });
  sleep(sleepSeconds(1));
}
