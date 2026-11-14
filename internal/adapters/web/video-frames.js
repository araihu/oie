// Only the browser's media decoder reads the source. Canvas holds a small JPEG.
export async function videoFrame(file, {signal, maxSide = 640} = {}) {
  signal?.throwIfAborted();
  const url = URL.createObjectURL(file);
  const video = document.createElement('video');
  video.preload = 'metadata';
  video.muted = true;
  video.playsInline = true;

  const events = new AbortController();
  let deadline, fallback, frame;
  try {
    return await new Promise((resolve, reject) => {
      const fail = () => reject(Error('video_frame_unavailable'));
      const listen = (name, callback) => video.addEventListener(name, callback, {signal: events.signal});
      let waiting = true, capturing = false;

      function draw() {
        clearTimeout(fallback);
        if (frame !== undefined) {
          video.cancelVideoFrameCallback(frame);
          frame = undefined;
        }
        try {
          const scale = Math.min(1, maxSide / Math.max(video.videoWidth, video.videoHeight));
          const canvas = document.createElement('canvas');
          canvas.width = Math.max(1, Math.round(video.videoWidth * scale));
          canvas.height = Math.max(1, Math.round(video.videoHeight * scale));
          const context = canvas.getContext('2d');
          context.drawImage(video, 0, 0, canvas.width, canvas.height);
          canvas.toBlob(blob => {
            if (blob?.type === 'image/jpeg' && blob.size > 0 && blob.size <= 1024 * 1024) resolve(blob);
            else fail();
          }, 'image/jpeg', 0.85);
        } catch {
          fail();
        }
      }

      function capture() {
        if (waiting || capturing || video.readyState < video.HAVE_CURRENT_DATA) return;
        capturing = true;
        if (video.requestVideoFrameCallback) {
          // A paused off-screen video can finish seeking before it submits the frame.
          frame = video.requestVideoFrameCallback(draw);
          fallback = setTimeout(draw, 250);
        } else draw();
      }

      listen('loadedmetadata', () => {
        if (!video.videoWidth || !video.videoHeight) { fail(); return; }
        const target = Number.isFinite(video.duration) ? Math.min(1, Math.max(0, video.duration - 0.1)) : 0;
        try {
          if (target === video.currentTime) video.preload = 'auto';
          video.currentTime = target;
          waiting = video.seeking;
          capture();
        } catch {
          fail();
        }
      });
      listen('seeked', () => { waiting = false; capture(); });
      listen('loadeddata', capture);
      listen('canplay', capture);
      listen('error', fail);
      signal?.addEventListener('abort', () => reject(signal.reason), {signal: events.signal, once: true});
      deadline = setTimeout(fail, 8000);
      video.src = url;
    });
  } finally {
    events.abort();
    clearTimeout(deadline);
    clearTimeout(fallback);
    if (frame !== undefined) video.cancelVideoFrameCallback(frame);
    try {
      video.removeAttribute('src');
      video.load();
    } finally {
      URL.revokeObjectURL(url);
    }
  }
}

export async function boundedVideoBlob(response, limit, type, signal) {
  const length = Number(response.headers.get('Content-Length'));
  if (!response.ok || !Number.isSafeInteger(length) || length <= 0 || length > limit || !response.body) {
    await response.body?.cancel();
    throw Error('video_sample_unavailable');
  }
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    while (true) {
      signal?.throwIfAborted();
      const {value, done} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > length) throw Error('video_sample_too_large');
      chunks.push(value);
    }
    if (size !== length) throw Error('video_sample_incomplete');
    return new Blob(chunks, {type});
  } finally {
    await reader.cancel().catch(() => {});
  }
}
