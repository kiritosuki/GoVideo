import { check, sleep } from 'k6';
import { commonReadOptions, ensureAccount, ensureVideo, postJSON, sleepSeconds } from './helpers.js';

export const options = commonReadOptions('feed_list_by_popularity');

export function setup() {
  // 热门榜在冷启动时可能没有缓存，准备视频后可同时观察缓存回源和后续命中表现。
  const account = ensureAccount(0);
  ensureVideo(account.token);
  return { token: account.token };
}

export default function (data) {
  const res = postJSON('/feed/listByPopularity', {
    limit: Number(__ENV.LIMIT || 10),
    as_of: 0,
    offset: 0,
  }, data.token, { api: 'feed_list_by_popularity' });

  check(res, {
    'listByPopularity status is 200': (r) => r.status === 200,
    'listByPopularity has video_list': (r) => Array.isArray(r.json('video_list')),
  });
  sleep(sleepSeconds(0.05));
}
