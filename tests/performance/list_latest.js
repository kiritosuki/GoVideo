import { check, sleep } from 'k6';
import { commonReadOptions, ensureAccount, ensureVideo, postJSON, sleepSeconds } from './helpers.js';

export const options = commonReadOptions('feed_list_latest');

export function setup() {
  // 准备一个登录用户和一条视频，保证空库启动时接口也有基础数据可查。
  const account = ensureAccount(0);
  ensureVideo(account.token);
  return { token: account.token };
}

export default function (data) {
  const res = postJSON('/feed/listLatest', {
    limit: Number(__ENV.LIMIT || 10),
    latest_time: 0,
  }, data.token, { api: 'feed_list_latest' });

  // 只校验接口成功和核心响应字段，压测阶段不做复杂业务断言。
  check(res, {
    'listLatest status is 200': (r) => r.status === 200,
    'listLatest has video_list': (r) => Array.isArray(r.json('video_list')),
  });
  sleep(sleepSeconds(0.05));
}
