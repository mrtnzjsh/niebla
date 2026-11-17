const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function main() {
  const defaultChromium = '/nix/store/1sda328rshrqarp3jsz1rd1h3bjj3fy7-chromium-155.0.8059.84/bin/chromium';
  const chromiumPath = process.env.CHROME_BIN || defaultChromium;
  const assetsDir = path.join(__dirname, '../src/assets/cotejo');
  if (!fs.existsSync(assetsDir)) {
    fs.mkdirSync(assetsDir, { recursive: true });
  }

  const outFull = process.argv[2] || path.join(assetsDir, 'cotejo-cl-full-review.png');
  const outDiff = process.argv[3] || path.join(assetsDir, 'cotejo-side-by-side-diff.png');
  const port = 9333;

  console.log(`Starting Chromium on port ${port}...`);
  const chrome = spawn(chromiumPath, [
    '--headless=new',
    '--no-sandbox',
    '--disable-gpu',
    `--remote-debugging-port=${port}`,
    '--window-size=1600,1100',
    'about:blank'
  ], {
    detached: true,
    stdio: 'ignore'
  });

  // Wait for remote debugging port
  let wsUrl = null;
  for (let i = 0; i < 30; i++) {
    await sleep(300);
    try {
      const res = await fetch(`http://127.0.0.1:${port}/json/version`);
      if (res.ok) {
        const data = await res.json();
        wsUrl = data.webSocketDebuggerUrl;
        break;
      }
    } catch (e) {}
  }

  if (!wsUrl) {
    console.error('Failed to connect to Chromium CDP port');
    process.kill(-chrome.pid);
    process.exit(1);
  }

  console.log('Connected to Chromium CDP. Creating target tab...');
  const tabRes = await fetch(`http://127.0.0.1:${port}/json/new?http://127.0.0.1:8080/cl/2`, { method: 'PUT' });
  const tabData = await tabRes.json();
  const pageWsUrl = tabData.webSocketDebuggerUrl;

  console.log('Connecting to page WebSocket...');
  const ws = new WebSocket(pageWsUrl);

  let msgId = 1;
  const callbacks = new Map();

  ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    if (data.id && callbacks.has(data.id)) {
      callbacks.get(data.id)(data);
      callbacks.delete(data.id);
    }
  };

  function sendCommand(method, params = {}) {
    return new Promise((resolve) => {
      const id = msgId++;
      callbacks.set(id, resolve);
      ws.send(JSON.stringify({ id, method, params }));
    });
  }

  await new Promise(resolve => ws.onopen = resolve);
  console.log('WebSocket opened. Enabling Page and Runtime...');

  await sendCommand('Page.enable');
  await sendCommand('Runtime.enable');

  // Wait for initial load
  console.log('Waiting for page load...');
  await sleep(2000);

  // Toggle diff accordion
  console.log('Triggering file accordion expand...');
  const evalRes = await sendCommand('Runtime.evaluate', {
    expression: `
      (function() {
        if (typeof toggleFileAccordion === 'function') {
          toggleFileAccordion(2, '-niebla-OWNERS-toml', '.niebla/OWNERS.toml', 1, 0);
          return "toggleFileAccordion called";
        }
        return "toggleFileAccordion not found";
      })()
    `
  });
  console.log('Eval result:', evalRes);

  // Wait for HTMX to fetch and render diff
  console.log('Waiting for diff content to load...');
  await sleep(3000);

  // Ensure we are at the top of the page
  console.log('Resetting scroll position to top...');
  await sendCommand('Runtime.evaluate', {
    expression: `window.scrollTo(0, 0);`
  });
  await sleep(500);

  // Capture full-page screenshot cleanly
  console.log('Capturing full-page screenshot...');
  const fullRes = await sendCommand('Page.captureScreenshot', {
    format: 'png',
    captureBeyondViewport: true
  });
  if (fullRes.result && fullRes.result.data) {
    const buffer = Buffer.from(fullRes.result.data, 'base64');
    fs.writeFileSync(outFull, buffer);
    console.log(`Full-page screenshot saved to ${outFull} (${buffer.length} bytes)`);
  }

  // Scroll into view so file diff header and code lines are prominent
  console.log('Scrolling diff into view...');
  await sendCommand('Runtime.evaluate', {
    expression: `
      (function() {
        const row = document.getElementById('accordion-row--niebla-OWNERS-toml');
        if (row) {
          const rect = row.getBoundingClientRect();
          window.scrollTo(0, window.scrollY + rect.top - 140);
        }
      })()
    `
  });
  await sleep(1000);

  // Take viewport screenshot (focused on diff)
  console.log('Capturing viewport screenshot with diff...');
  const screenshotRes = await sendCommand('Page.captureScreenshot', {
    format: 'png',
    captureBeyondViewport: false
  });

  if (screenshotRes.result && screenshotRes.result.data) {
    const buffer = Buffer.from(screenshotRes.result.data, 'base64');
    fs.writeFileSync(outDiff, buffer);
    console.log(`Viewport diff screenshot saved to ${outDiff} (${buffer.length} bytes)`);
  }

  ws.close();
  try {
    process.kill(-chrome.pid);
  } catch (e) {
    chrome.kill();
  }
}

main().catch(err => {
  console.error('Error in script:', err);
  process.exit(1);
});
