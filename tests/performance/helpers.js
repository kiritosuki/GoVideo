import http from 'k6/http';
import { check, fail } from 'k6';

export const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
export const DEFAULT_PASSWORD = __ENV.PERF_PASSWORD || '123456';

export function jsonHeaders(token) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  return headers;
}

export function postJSON(path, body, token, tags) {
  return http.post(`${BASE_URL}${path}`, JSON.stringify(body || {}), {
    headers: jsonHeaders(token),
    tags: tags || {},
  });
}

export function parseJSON(res, label) {
  try {
    return res.json();
  } catch (err) {
    fail(`${label} response is not json: status=${res.status} body=${res.body}`);
  }
}

export function ensureAccount(index = 0) {
  const username = __ENV.PERF_USERNAME || `perf_user_${index}`;
  const password = __ENV.PERF_PASSWORD || DEFAULT_PASSWORD;

  // 先尝试登录，避免重复运行压测时不断消耗注册限流次数。
  let res = postJSON('/account/login', { username, password }, null, { api: 'account_login' });
  if (res.status !== 200) {
    res = postJSON('/account/register', { username, password }, null, { api: 'account_register' });
    check(res, {
      'register success or already exists': (r) => r.status === 200 || r.status === 409,
    });

    res = postJSON('/account/login', { username, password }, null, { api: 'account_login' });
  }

  check(res, {
    'login status is 200': (r) => r.status === 200,
  });
  if (res.status !== 200) {
    fail(`login failed: status=${res.status} body=${res.body}`);
  }

  const body = parseJSON(res, 'login');
  if (!body.token) {
    fail(`login response has no token: ${res.body}`);
  }
  return {
    username,
    password,
    token: body.token,
    accountID: body.account_id || body.accountID || 0,
  };
}

export function publishVideo(token, index = 0) {
  const title = `perf-video-${index}`;
  const payload = {
    title,
    description: 'performance test seed video',
    play_url: `https://example.com/videos/${title}.mp4`,
    cover_url: `https://example.com/covers/${title}.jpg`,
  };
  const res = postJSON('/video/publish', payload, token, { api: 'video_publish' });
  check(res, {
    'publish video status is 200': (r) => r.status === 200,
  });
  if (res.status !== 200) {
    fail(`publish video failed: status=${res.status} body=${res.body}`);
  }

  const body = parseJSON(res, 'video publish');
  if (!body.id) {
    fail(`publish video response has no id: ${res.body}`);
  }
  return body.id;
}

export function ensureVideo(token) {
  if (__ENV.VIDEO_ID) {
    return Number(__ENV.VIDEO_ID);
  }
  return publishVideo(token, Date.now());
}

export function commonReadOptions(apiName) {
  return {
    scenarios: {
      steady: {
        executor: 'constant-vus',
        vus: Number(__ENV.VUS || 5),
        duration: __ENV.DURATION || '30s',
      },
    },
    thresholds: {
      [`http_req_failed{api:${apiName}}`]: ['rate<0.01'],
      [`http_req_duration{api:${apiName}}`]: ['p(95)<500', 'p(99)<1000'],
    },
  };
}

export function sleepSeconds(defaultValue) {
  return Number(__ENV.SLEEP || defaultValue);
}
