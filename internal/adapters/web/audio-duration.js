export const waveformDurationLimit = 5 * 60;

export async function audioPeaks(blob, signal) {
  const bytes = await blob.arrayBuffer();
  signal.throwIfAborted();
  // Offline decoding works before a playback gesture, including in WebKit.
  const audio = await new OfflineAudioContext(1, 1, 8000).decodeAudioData(bytes);
  signal.throwIfAborted();
  const peaks = new Float32Array(Math.min(1024, audio.length));
  for (let channel = 0; channel < audio.numberOfChannels; channel++) {
    const samples = audio.getChannelData(channel);
    let start = 0;
    for (let bin = 0; bin < peaks.length; bin++) {
      const end = Math.floor((bin + 1) * samples.length / peaks.length);
      for (let sample = start; sample < end; sample++) {
        peaks[bin] = Math.max(peaks[bin], Math.abs(samples[sample]));
      }
      start = end;
    }
  }
  return [peaks];
}

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
    if (!Number.isFinite(media.duration) && blob.type === 'audio/webm') {
      await audioEvent(media, 'seeked', () => { media.currentTime = Number.MAX_SAFE_INTEGER; }, signal);
    }
    return media.duration;
  } finally {
    media.removeAttribute('src');
    media.load();
    URL.revokeObjectURL(url);
  }
}
