const net = require('node:net');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { mkdtemp, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');

async function reserveLoopbackAddress() {
  const probe = net.createServer().listen(0, '127.0.0.1');
  await once(probe, 'listening');
  return {
    listen: '127.0.0.1:' + probe.address().port,
    release: () => new Promise(resolve => probe.close(resolve)),
  };
}

async function loopbackAddress() {
  const {listen, release} = await reserveLoopbackAddress();
  await release();
  return listen;
}

async function startOie(binary, args, readyPath, configure, startupTimeout = 15000) {
  const dir = await mkdtemp(path.join(tmpdir(), 'oie-browser-'));
  let listen, options;
  try {
    const reservation = await reserveLoopbackAddress();
    listen = reservation.listen;
    try {
      options = await configure?.({listen, dir});
    } finally {
      // Hosted configuration starts the issuer and chooses the probe address.
      await reservation.release();
    }
  } catch (error) {
    await rm(dir, {recursive: true, force: true});
    throw error;
  }
  const child = spawn(binary, [...args, ...(options?.args ?? []), '--listen', listen, '--data-dir', dir], {
    env: options?.env,
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let stderr = '', spawnError;
  child.stderr.setEncoding('utf8').on('data', line => {
    stderr = (stderr + line).slice(-8192);
  });
  child.once('error', error => { spawnError = error; });
  const closed = new Promise(resolve => child.once('close', resolve));
  const url = options?.url ?? 'http://' + listen;
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
    const deadline = Date.now() + startupTimeout;
    while (Date.now() < deadline) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null || child.signalCode !== null) break;
      try {
        const response = await fetch((options?.readyOrigin ?? url) + readyPath, { signal: AbortSignal.timeout(1000) });
        await response.body?.cancel();
        if (response.ok) return { url, dir, stop, stderr: () => stderr };
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

module.exports = { loopbackAddress, startOie };
