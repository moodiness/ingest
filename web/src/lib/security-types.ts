export interface SecurityStatus {
  mfa_enabled: boolean
  recovery_codes_remaining: number
  pending_expires_at?: string
}

export interface AdminSession {
  id: string
  user_agent: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}

export interface SecurityAudit {
  id: string
  action:
    | 'security.mfa_enabled'
    | 'security.mfa_disabled'
    | 'security.recovery_rotated'
    | 'security.session_revoked'
    | 'security.sessions_revoked'
    | 'share.created'
    | 'share.permissions_changed'
    | 'share.rotated'
    | 'share.revoked'
  target_id: string
  created_at: string
}

export interface MFAEnrollment {
  secret: string
  otpauth_url: string
  expires_at: string
}

export interface RecoveryCodes {
  recovery_codes: string[]
}
