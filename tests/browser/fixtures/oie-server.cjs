const net = require('node:net');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { mkdtemp, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');

async function startOie(binary, args, readyPath) {
  const probe = net.createServer().listen(0, '127.0.0.1');
  await once(probe, 'listening');
  const listen = '127.0.0.1:' + probe.address().port;
  await new Promise(resolve => probe.close(resolve));

  const dir = await mkdtemp(path.join(tmpdir(), 'oie-browser-'));
  const child = spawn(binary, [...args, '--listen', listen, '--data-dir', dir], {
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '', spawnError;
  child.stderr.setEncoding('utf8').on('data', line => {
    stderr = (stderr + line).slice(-8192);
  });
  child.once('error', error => { spawnError = error; });
  const closed = new Promise(resolve => child.once('close', resolve));
  const url = 'http://' + listen;
  const stop = async () => {
    child.kill();
    const deadline = setTimeout(() => child.kill('SIGKILL'), 8000);
    try {
      await closed;
    } finally {
      clearTimeout(deadline);
      await rm(dir, { recursive: true, force: true });
    }
  };

  try {
    const deadline = Date.now() + 15000;
    while (Date.now() < deadline) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null || child.signalCode !== null) break;
      try {
        const response = await fetch(url + readyPath, { signal: AbortSignal.timeout(1000) });
        await response.body?.cancel();
        if (response.ok) return { url, stop };
      } catch {
        // The listener can become ready after the first connection attempt.
      }
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error('Oiê did not become ready\n' + stderr);
  } catch (error) {
    await stop();
    throw error;
  }
}

module.exports = { startOie };
