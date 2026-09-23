// Sign-in with an identity provider: which providers are on, and what to say
// when the round trip through one comes back refused.
import { useEffect, useState } from 'react'
import { auth } from '../api'
import type { OAuthProvider } from '../api'

// The gateway sends back a code, never a provider's own words, so every
// message a person can see here is one written on purpose.
export function oauthErrorText(code: string | null, providerID: string | null, registration = false): string | null {
  if (!code) return null
  const name = providerName(providerID)
  switch (code) {
    case 'no_account':
      return registration
        ? `There is no account here for the address on your ${name} account. Create one with that address and a password, then connect ${name} from your account page.`
        : `There is no account here for the address on your ${name} account. Accounts are by invitation — ask an admin of your organization for a link.`
    case 'email_unverified':
      return `${name} has not confirmed that address belongs to you, so it cannot be used to find your account. Sign in with your password, then connect ${name} from your account page.`
    case 'invite_email_mismatch':
      return `This invitation was sent to a different address than the one ${name} confirmed for you. Sign in with the account the invitation was sent to.`
    case 'invite_invalid':
      return 'This invitation is no longer valid — it was used, revoked, or it expired.'
    case 'identity_taken':
      return `That ${name} account is already connected to a different Aperture account.`
    case 'denied':
      return `The ${name} sign-in was cancelled.`
    case 'state':
    case 'session':
      return 'The sign-in took too long or was started in another tab. Please try again.'
    case 'disabled':
      return 'This account is disabled.'
    case 'provider':
      return `${name} did not complete the sign-in. Please try again in a moment.`
    case 'not_configured':
      return 'That sign-in method is not set up on this installation.'
    default:
      return 'The sign-in did not go through. Please try again.'
  }
}

export function providerName(id: string | null): string {
  switch (id) {
    case 'google':
      return 'Google'
    case 'github':
      return 'GitHub'
    case 'yandex':
      return 'Yandex'
    default:
      return 'the provider'
  }
}

export interface SignInOptions {
  providers: OAuthProvider[]
  /** Anybody may sign up; undefined until the gateway has answered. */
  registration?: boolean
}

/** What the sign-in pages may offer, asked once per page. */
export function useSignInOptions(): SignInOptions {
  const [options, setOptions] = useState<SignInOptions>({ providers: [] })
  useEffect(() => {
    let live = true
    auth
      .providers()
      .then((r) => live && setOptions({ providers: r.providers, registration: !!r.registration }))
      .catch(() => live && setOptions({ providers: [], registration: false }))
    return () => {
      live = false
    }
  }, [])
  return options
}

/** Which providers are on, asked once per page. */
export function useProviders(): OAuthProvider[] {
  return useSignInOptions().providers
}

