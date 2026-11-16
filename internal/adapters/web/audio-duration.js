export const waveformDurationLimit = 5 * 60;

async function audioEvent(media, type, start, signal) {
  signal.throwIfAborted();
  const listeners = new AbortController();
  let timeout;
  try {
    await new Promise((resolve, reject) => {
      const options = {signal: listeners.signal, once: true};
      const unavailable = () => reject(Error('audio metadata unavailable'));
      media.addEventListener(type, resolve, options);
      media.addEventListener('error', unavailable, options);
      signal.addEventListener('abort', () => reject(signal.reason), options);
      timeout = setTimeout(unavailable, 5000);
      start();
    });
  } finally {
    clearTimeout(timeout);
    listeners.abort();
  }
}

export async function audioDuration(blob, signal) {
  signal.throwIfAborted();
  const media = document.createElement('audio');
  const url = URL.createObjectURL(blob);
  media.preload = 'metadata';
  try {
    await audioEvent(media, 'loadedmetadata', () => { media.src = url; }, signal);
    // MediaRecorder's WebM output can omit the duration until the end is read.
    if (media.duration === Infinity && blob.type === 'audio/webm') {
      await audioEvent(media, 'seeked', () => { media.currentTime = Number.MAX_SAFE_INTEGER; }, signal);
    }
    return media.duration;
  } finally {
    media.removeAttribute('src');
    media.load();
    URL.revokeObjectURL(url);
  }
}
