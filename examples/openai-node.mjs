// Aperture DLP gateway with the official OpenAI Node SDK.
// The only change vs. talking to OpenAI directly: baseURL + your aperture key.
//
//   npm install openai
//   APERTURE_API_KEY=ap-... node openai-node.mjs

import OpenAI from 'openai'

const client = new OpenAI({
  baseURL: (process.env.APERTURE_URL || 'http://localhost:8080') + '/v1',
  apiKey: process.env.APERTURE_API_KEY, // aperture key, not a provider key
})

// Clean request — proxied to the provider as usual.
const resp = await client.chat.completions.create({
  model: 'gpt-4o-mini',
  messages: [{ role: 'user', content: 'Say hi in five words' }],
})
console.log('clean:', resp.choices[0].message.content)

// A secret in the prompt — Aperture blocks it before it leaves your network.
try {
  await client.chat.completions.create({
    model: 'gpt-4o-mini',
    messages: [{ role: 'user', content: 'Deploy with AKIAIOSFODNN7EXAMPLE' }],
  })
} catch (err) {
  console.log('blocked by DLP:', err.status, err.error?.message)
}
