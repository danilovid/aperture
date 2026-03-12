#!/usr/bin/env node
import { chromium } from 'playwright';

const API_URL = process.env.VITE_APERTURE_URL || 'http://localhost:8081';
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

// Set key via fill (simulates input)
await page.fill('.modal-input', TEST_KEY);

// Save
await page.click('button.modal-btn[type="submit"]');
await page.waitForTimeout(800);

// Verify via API
const res = await fetch(`${API_URL}/admin/config`);
const { configured } = await res.json();

console.log('API configured:', configured);

// Test chat would fail (invalid key) but flow works
const chatRes = await fetch(`${API_URL}/v1/chat/completions`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer dev' },
  body: JSON.stringify({ model: 'gpt-4o-mini', messages: [{ role: 'user', content: 'Hi' }], stream: false }),
});
const chatBody = await chatRes.json();
const gotOpenAIResponse = chatBody.error?.message?.includes('Incorrect API key') || chatRes.status === 200;

console.log('Chat proxied to OpenAI:', gotOpenAIResponse);
console.log(configured && gotOpenAIResponse ? '\n✓ All checks passed' : '\n✗ Some checks failed');

await browser.close();
process.exit(configured && gotOpenAIResponse ? 0 : 1);
