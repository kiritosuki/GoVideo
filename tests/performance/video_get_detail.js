import { check, sleep } from 'k6';
import { commonReadOptions, ensureAccount, ensureVideo, postJSON, sleepSeconds } from './helpers.js';

export const options = commonReadOptions('video_get_detail');

export function setup() {
  // video_id 可以通过环境变量指定；未指定时自动发布一条视频作为压测目标。
  const account = ensureAccount(0);
  const videoID = ensureVideo(account.token);
  return { videoID };
}

export default function (data) {
  const res = postJSON('/video/getDetail', {
    id: data.videoID,
  }, null, { api: 'video_get_detail' });

  check(res, {
    'getDetail status is 200': (r) => r.status === 200,
    'getDetail id matches': (r) => Number(r.json('id')) === Number(data.videoID),
  });
  sleep(sleepSeconds(0.05));
}
