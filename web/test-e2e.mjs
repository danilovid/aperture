#!/usr/bin/env node
// E2E: requires the gateway running with known keys, e.g.
//   APERTURE_API_KEY=ap-test ADMIN_API_KEY=admin-test PORT=8081 go run ./cmd/aperture
//   APERTURE_API_KEY=ap-test ADMIN_API_KEY=admin-test node test-e2e.mjs
import { chromium } from 'playwright';

const API_URL = process.env.VITE_APERTURE_URL || 'http://localhost:8081';
const APERTURE_KEY = process.env.APERTURE_API_KEY || 'ap-test';
const ADMIN_KEY = process.env.ADMIN_API_KEY || 'admin-test';
const TEST_KEY = 'sk-proj-test-random-key-' + Date.now();

console.log('Test key:', TEST_KEY.slice(0, 25) + '...');
console.log('API URL:', API_URL);

const browser = await chromium.launch();
const page = await browser.newPage();

await page.goto('http://localhost:5173');
await page.waitForLoadState('networkidle');

// Open settings
await page.click('button.settings-btn');
await page.waitForSelector('.modal', { state: 'visible' });

// Fill aperture/admin keys (first two password inputs), then the OpenAI key
const inputs = page.locator('.modal input.modal-input');
await inputs.nth(0).fill(APERTURE_KEY);
await inputs.nth(1).fill(ADMIN_KEY);
await inputs.nth(2).fill(TEST_KEY);

// Save
await page.click('button.modal-btn[type="submit"]');
await page.waitForTimeout(800);

// Verify via API (admin endpoints now require the admin key)
const res = await fetch(`${API_URL}/admin/config`, {
  headers: { Authorization: `Bearer ${ADMIN_KEY}` },
});
const { configured } = await res.json();

console.log('API configured:', configured);

// Unauthorized admin access must be rejected
const unauthRes = await fetch(`${API_URL}/admin/config`);
const adminClosed = unauthRes.status === 401;
console.log('Admin closed without key:', adminClosed);

// Chat with a wrong aperture key must be rejected
const badChat = await fetch(`${API_URL}/v1/chat/completions`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: 'Bearer wrong-key' },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [{ role: 'user', content: 'Hi' }], stream: false }),
});
const chatAuthEnforced = badChat.status === 401;
console.log('Chat rejects wrong aperture key:', chatAuthEnforced);

// Test chat would fail upstream (invalid OpenAI key) but the flow works
const chatRes = await fetch(`${API_URL}/v1/chat/completions`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${APERTURE_KEY}` },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [{ role: 'user', content: 'Hi' }], stream: false }),
});
const chatBody = await chatRes.json();
const gotOpenAIResponse = chatBody.error?.message?.includes('Incorrect API key') || chatRes.status === 200;

console.log('Chat proxied to OpenAI:', gotOpenAIResponse);
const ok = configured && adminClosed && chatAuthEnforced && gotOpenAIResponse;
console.log(ok ? '\n✓ All checks passed' : '\n✗ Some checks failed');

await browser.close();
process.exit(ok ? 0 : 1);
