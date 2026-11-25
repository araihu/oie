// Append byte ranges from the retained original. The server strips provider
// URLs from the index, so neither the player nor a seek contacts the provider.
const indexType = 'application/vnd.oie.video-index+json';
const maxFragment = 32 * 1024 * 1024;
const aheadSeconds = 12;
const behindSeconds = 24;

function layout(index) {
 const {codecs, size, initLength, segments} = index;
 const validLength = value => Number.isSafeInteger(value) && value > 0 && value <= maxFragment;
 if (!/^[A-Za-z0-9.,-]{1,128}$/.test(codecs) ||
     !Number.isSafeInteger(size) || size > 2 ** 31 || !validLength(initLength) ||
     !Array.isArray(segments) || !segments.length || segments.length > 16000) {
  throw new Error('Invalid video index');
 }
 let offset = initLength, time = 0;
 const ranges = segments.map(({duration, length}) => {
  if (!validLength(length) || !Number.isFinite(duration) || duration <= 0 || duration > 60) {
   throw new Error('Invalid video fragment');
  }
  const start = offset;
  offset += length;
  time += duration;
  return {start, end:offset - 1, until:time};
 });
 if (offset !== size) throw new Error('Incomplete video index');
 return {mime:'video/mp4; codecs="' + codecs + '"', duration:time, ranges};
}

function segmentAt(ranges, time) {
 let low = 0, high = ranges.length - 1;
 while (low < high) {
  const mid = Math.floor((low + high) / 2);
  if (ranges[mid].until <= time) low = mid + 1;
  else high = mid;
 }
 return low;
}

function awaitEvent(target, event, signal, action) {
 signal.throwIfAborted();
 return new Promise((resolve, reject) => {
  const subscriptions = new AbortController();
  const settle = (callback, value) => {
   subscriptions.abort();
   callback(value);
  };
  const options = {once:true, signal:subscriptions.signal};
  signal.addEventListener('abort', () => settle(reject, signal.reason), options);
  target.addEventListener('error', () => settle(reject, new Error('Video buffer unavailable')), options);
  target.addEventListener(event, () => settle(resolve), options);
  try { action(); } catch (error) { settle(reject, error); }
 });
}

export async function playIndexed({video, url, signal, onFailure, onPlayBlocked}) {
 const MediaSourceType = window.MediaSource || window.ManagedMediaSource;
 if (!MediaSourceType) throw new Error('Media Source unavailable');
 const media = new MediaSourceType();
 const objectURL = URL.createObjectURL(media);
 const remotePlayback = video.disableRemotePlayback;
 if (MediaSourceType === window.ManagedMediaSource) video.disableRemotePlayback = true;
 signal.addEventListener('abort', () => {
  URL.revokeObjectURL(objectURL);
  video.disableRemotePlayback = remotePlayback;
 }, {once:true});

 video.addEventListener('error', onFailure, {signal});
 const indexURL = new URL(url);
 indexURL.searchParams.set('index', '1');
 const startup = AbortSignal.any([signal, AbortSignal.timeout(15000)]);
 const [index] = await Promise.all([
  fetch(indexURL, {signal:startup, redirect:'error'}).then(response => {
   if (!response.ok || response.headers.get('Content-Type') !== indexType) {
    void response.body?.cancel().catch(() => {});
    throw new Error('Video index unavailable');
   }
   return response.json();
  }),
  awaitEvent(media, 'sourceopen', startup, () => {
   video.src = objectURL;
   video.play()?.catch(error => {
    if (signal.aborted || error.name === 'AbortError') return;
    if (error.name === 'NotAllowedError') onPlayBlocked();
    else onFailure();
   });
  }),
 ]);

 const {mime, duration, ranges} = layout(index);
 if (!MediaSourceType.isTypeSupported(mime)) throw new Error('Video codec unavailable');
 const buffer = media.addSourceBuffer(mime);
 const mutate = action => awaitEvent(buffer, 'updateend', signal, action);
 let validator;
 async function range(start, end, requestSignal) {
  const headers = {Range:'bytes=' + start + '-' + end};
  if (validator?.value) headers['If-Range'] = validator.value;
  const response = await fetch(url, {headers, redirect:'error',
   signal:AbortSignal.any([requestSignal, AbortSignal.timeout(15000)])});
  if (response.status !== 206 || response.headers.get('Content-Type') !== 'video/mp4' ||
      response.headers.get('Content-Range') !== 'bytes ' + start + '-' + end + '/' + index.size ||
      validator?.value && response.headers.get(validator.header) !== validator.value) {
   void response.body?.cancel().catch(() => {});
   throw new Error('Unexpected video range');
  }
  if (!validator) {
   const tag = response.headers.get('ETag');
   validator = tag && !tag.startsWith('W/') ? {header:'ETag', value:tag} :
    {header:'Last-Modified', value:response.headers.get('Last-Modified')};
  }
  const bytes = await response.arrayBuffer();
  if (bytes.byteLength !== end - start + 1) throw new Error('Incomplete video fragment');
  return bytes;
 }

 const init = await range(0, index.initLength - 1, signal);
 await mutate(() => buffer.appendBuffer(init));
 signal.throwIfAborted();
 media.duration = duration;

 let next = 0, running = false, wanted = false, reset = false, request;
 const bufferedThrough = time => {
  for (let i = 0; i < video.buffered.length; i++) {
   if (video.buffered.start(i) <= time + 0.05 && video.buffered.end(i) > time) return video.buffered.end(i);
  }
  return time;
 };
 async function pump() {
  wanted = true;
  if (running || signal.aborted) return;
  running = true;
  try {
   while (wanted && !signal.aborted) {
    wanted = false;
    const time = video.currentTime;
    const through = bufferedThrough(time);
    if (reset) {
     reset = false;
     if (through === time && buffer.buffered.length) await mutate(() => buffer.remove(0, duration));
     next = segmentAt(ranges, Math.max(time, through));
     if (wanted) continue;
    }
    if (time > behindSeconds && buffer.buffered.length && buffer.buffered.start(0) < time - behindSeconds - 4) {
     await mutate(() => buffer.remove(0, time - behindSeconds));
     wanted = true;
     continue;
    }
    if (through - time >= aheadSeconds || next >= ranges.length) continue;
    const fragment = ranges[next];
    request = new AbortController();
    let bytes;
    try {
     bytes = await range(fragment.start, fragment.end, AbortSignal.any([signal, request.signal]));
    } catch (error) {
     if (request.signal.aborted) continue;
     throw error;
    } finally {
     request = null;
    }
    signal.throwIfAborted();
    if (reset) continue;
    await mutate(() => buffer.appendBuffer(bytes));
    next++;
    if (next === ranges.length && !reset) media.endOfStream();
    wanted = true;
   }
  } catch {
   if (!signal.aborted) onFailure();
  } finally {
   running = false;
  }
 }
 video.addEventListener('seeking', () => {
  reset = true;
  request?.abort();
  void pump();
 }, {signal});
 for (const event of ['timeupdate', 'waiting', 'play']) {
  video.addEventListener(event, () => { void pump(); }, {signal});
 }
 void pump();
}
