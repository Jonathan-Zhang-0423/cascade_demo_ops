import { useEffect, useState, type FormEvent } from "react";
import type { DesktopBridgeClient } from "./bridge";
import type { AccountPlanView, AccountProfileView, AccountSessionView, AccountVerificationChannel, AccountVerificationStartView, GitHubDeviceFlowView } from "./domain";
import { AccountAvatar } from "./AccountMenu";

export function LoginScreen({ bridge, session, initialError, onSessionChange }: { bridge: DesktopBridgeClient; session: AccountSessionView; initialError: string; onSessionChange: (session: AccountSessionView) => void }) {
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [setupConfirm, setSetupConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(initialError);
  const [recovering, setRecovering] = useState(false);
  const [resetDestination, setResetDestination] = useState("");
  const [resetFlow, setResetFlow] = useState<AccountVerificationStartView>();
  const [resetCode, setResetCode] = useState("");
  const [newPassword, setNewPassword] = useState("");

  async function signIn(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setBusy(true); setError("");
    const result = await bridge.passwordAccountLogin(identifier, password);
    setBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Sign-in failed."); return; }
    onSessionChange(result.data);
  }
  async function completeFirstRunSetup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError("");
    if (password !== setupConfirm) { setError("Passwords do not match."); return; }
    setBusy(true); const result = await bridge.setAccountPassword(password); setBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Password could not be created."); return; }
    setPassword(""); setSetupConfirm(""); setError("Password created. Sign in to continue."); onSessionChange(result.data);
  }
  async function devSignIn() {
    setBusy(true); setError(""); const result = await bridge.devAccountLogin(); setBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Development sign-in failed."); return; }
    onSessionChange(result.data);
  }
  async function startReset() {
    setBusy(true); setError(""); const result = await bridge.startAccountPasswordReset("email", resetDestination.trim()); setBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Password reset could not start."); return; }
    setResetFlow(result.data);
  }
  async function confirmReset() {
    setBusy(true); setError(""); const result = await bridge.confirmAccountPasswordReset("email", resetDestination.trim(), resetCode, newPassword); setBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Password reset could not be completed."); return; }
    setRecovering(false); setResetFlow(undefined); setResetCode(""); setNewPassword(""); setPassword("");
    setError("Password updated. Sign in with your new password."); onSessionChange(result.data);
  }

  return <main className="account-login-shell">
    <div className="account-login-card">
      <img src="/Logo_full_black.png" alt="Cascade" />
      <div><h1>{session.passwordSetupRequired && !session.devLoginAvailable ? "Secure your account" : recovering ? "Reset password" : "Welcome back"}</h1><p>{session.passwordSetupRequired && !session.devLoginAvailable ? "Create the first password for this local Cascade account." : recovering ? "Use your verified email to recover access." : "Sign in to continue building evidence-backed product demos."}</p></div>
      {error ? <div className="account-inline-error" role="alert">{error}</div> : null}
      {session.passwordSetupRequired && !session.devLoginAvailable ? <form className="account-login-form" onSubmit={completeFirstRunSetup}>
        <label><span>Account</span><input readOnly value={session.user?.displayName ?? "Local user"} /></label>
        <label><span>New password</span><input required type="password" minLength={12} maxLength={128} autoComplete="new-password" value={password} onChange={(event) => setPassword(event.currentTarget.value)} /><small>12–128 characters</small></label>
        <label><span>Confirm password</span><input required type="password" minLength={12} maxLength={128} autoComplete="new-password" value={setupConfirm} onChange={(event) => setSetupConfirm(event.currentTarget.value)} /></label>
        <button type="submit" className="account-primary-button" disabled={busy || password.length < 12 || setupConfirm.length < 12}>{busy ? "Securing…" : "Create password"}</button>
      </form> : !recovering ? <form className="account-login-form" onSubmit={signIn}>
        <label><span>Email or username</span><input required autoComplete="username" value={identifier} onChange={(event) => setIdentifier(event.currentTarget.value)} /></label>
        <label><span>Password</span><input required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.currentTarget.value)} /></label>
        <button type="submit" className="account-primary-button" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</button>
        <button type="button" className="account-text-button" onClick={() => { setRecovering(true); setError(""); }}>Forgot password?</button>
      </form> : <div className="account-login-form">
        <label><span>Verified email</span><input value={resetDestination} onChange={(event) => setResetDestination(event.currentTarget.value)} type="email" autoComplete="email" /></label>
        {!resetFlow ? <button type="button" className="account-primary-button" disabled={busy || !resetDestination.trim()} onClick={startReset}>{busy ? "Sending…" : "Send recovery code"}</button> : <>
          <p className="account-reset-note">If that destination belongs to this account, a code was sent to {resetFlow.maskedDestination}.</p>
          {resetFlow.developmentCode ? <small>Development code: <code>{resetFlow.developmentCode}</code></small> : null}
          <label><span>Verification code</span><input inputMode="numeric" autoComplete="one-time-code" value={resetCode} onChange={(event) => setResetCode(event.currentTarget.value.replace(/\D/g, "").slice(0, 6))} /></label>
          <label><span>New password</span><input type="password" minLength={12} maxLength={128} autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.currentTarget.value)} /><small>12–128 characters</small></label>
          <button type="button" className="account-primary-button" disabled={busy || resetCode.length !== 6 || newPassword.length < 12} onClick={confirmReset}>{busy ? "Updating…" : "Reset password"}</button>
        </>}
        <button type="button" className="account-text-button" onClick={() => { setRecovering(false); setResetFlow(undefined); setError(""); }}>Back to sign in</button>
      </div>}
      {session.devLoginAvailable && !recovering ? <><div className="account-login-divider"><span>Development</span></div><button type="button" className="account-secondary-button" disabled={busy} onClick={devSignIn}>Continue with local account</button><small>Development bypass · unavailable in production</small></> : null}
    </div>
  </main>;
}

export function AccountLoadingScreen() {
  return <main className="account-login-shell"><div className="account-loading-mark"><span />Restoring your Cascade workspace…</div></main>;
}

export function ProfilePanel({ bridge, profile, onProfileChange, onSessionChange, onLogout }: { bridge: DesktopBridgeClient; profile: AccountProfileView; onProfileChange: (profile: AccountProfileView) => void; onSessionChange: (session: AccountSessionView) => void; onLogout: () => void }) {
  const [displayName, setDisplayName] = useState(profile.displayName);
  const [email, setEmail] = useState(profile.email ?? "");
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [flow, setFlow] = useState<GitHubDeviceFlowView>();
  const [githubBusy, setGitHubBusy] = useState(false);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);
  const [verificationBusy, setVerificationBusy] = useState<AccountVerificationChannel>();
  const [verificationFlows, setVerificationFlows] = useState<Partial<Record<AccountVerificationChannel, AccountVerificationStartView>>>({});
  const [verificationCodes, setVerificationCodes] = useState<Partial<Record<AccountVerificationChannel, string>>>({ email: "" });
  const [verificationNow, setVerificationNow] = useState(() => Date.now());
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);

  useEffect(() => {
    setDisplayName(profile.displayName);
    setEmail(profile.email ?? "");
  }, [profile]);

  useEffect(() => {
    let active = true;
    bridge.accountVerificationState("email").then((result) => {
      if (!active || !result.ok || !result.data || result.data.status !== "pending" || !result.data.maskedDestination || !result.data.expiresAt || !result.data.resendAt) return;
      setVerificationFlows((current) => ({ ...current, email: { channel: "email", status: "pending", maskedDestination: result.data!.maskedDestination!, expiresAt: result.data!.expiresAt!, resendAt: result.data!.resendAt! } }));
    });
    return () => { active = false; };
  }, [bridge, profile.id]);

  useEffect(() => {
    if (!verificationFlows.email) return;
    const timer = window.setInterval(() => setVerificationNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [verificationFlows.email]);

  useEffect(() => {
    if (!flow || flow.status !== "pending") return;
    const timer = window.setInterval(async () => {
      const result = await bridge.pollGitHubDeviceFlow(flow.id);
      if (!result.ok || !result.data) {
        setError(result.error ?? "GitHub authorization status is unavailable.");
        return;
      }
      setFlow(result.data);
      if (result.data.status === "authorized") {
        const refreshed = await bridge.accountProfile();
        if (refreshed.ok && refreshed.data) onProfileChange(refreshed.data);
        setMessage("GitHub account connected.");
      }
    }, Math.max(1000, flow.intervalSeconds * 1000));
    return () => window.clearInterval(timer);
  }, [bridge, flow?.id, flow?.status, flow?.intervalSeconds, onProfileChange]);

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true); setError(""); setMessage("");
    const result = await bridge.updateAccountProfile({ displayName });
    setSaving(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Profile could not be saved."); return; }
    onProfileChange(result.data); setMessage("Profile saved.");
  }

  async function connectGitHub() {
    setGitHubBusy(true); setError(""); setMessage("");
    const result = await bridge.startGitHubDeviceFlow();
    setGitHubBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "GitHub authorization could not start."); return; }
    setFlow(result.data);
  }

  async function disconnectGitHub() {
    setGitHubBusy(true); setError("");
    const result = await bridge.disconnectAccountGitHub();
    setGitHubBusy(false); setConfirmDisconnect(false); setFlow(undefined);
    if (!result.ok || !result.data) { setError(result.error ?? "GitHub account could not be disconnected."); return; }
    onProfileChange(result.data); setMessage("GitHub account disconnected.");
  }

  async function beginVerification(channel: AccountVerificationChannel) {
    const destination = email.trim();
    setVerificationBusy(channel); setError(""); setMessage("");
    const result = await bridge.startAccountVerification(channel, destination);
    setVerificationBusy(undefined);
    if (!result.ok || !result.data) { setError(verificationErrorMessage(result.errorInfo?.code, result.error)); return; }
    const verificationFlow = result.data;
    setVerificationFlows({ email: verificationFlow });
    setMessage(`Verification code sent to ${verificationFlow.maskedDestination}.`);
  }

  async function confirmVerification(channel: AccountVerificationChannel) {
    setVerificationBusy(channel); setError(""); setMessage("");
    const result = await bridge.confirmAccountVerification(channel, verificationCodes[channel] ?? "");
    setVerificationBusy(undefined);
    if (!result.ok || !result.data) { setError(verificationErrorMessage(result.errorInfo?.code, result.error)); return; }
    onProfileChange(result.data);
    setVerificationFlows({});
    setVerificationCodes({ email: "" });
    setMessage("Email verified.");
  }

  async function savePassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError(""); setMessage("");
    if (newPassword !== confirmPassword) { setError("New passwords do not match."); return; }
    setPasswordBusy(true);
    const result = profile.hasPassword ? await bridge.changeAccountPassword(currentPassword, newPassword) : await bridge.setAccountPassword(newPassword);
    setPasswordBusy(false);
    if (!result.ok || !result.data) { setError(result.error ?? "Password could not be updated."); return; }
    onSessionChange(result.data);
  }

  const dirty = displayName.trim() !== profile.displayName;
  const emailResendSeconds = verificationFlows.email ? Math.max(0, Math.ceil((new Date(verificationFlows.email.resendAt).getTime() - verificationNow) / 1000)) : 0;
  const emailExpired = verificationFlows.email ? verificationNow >= new Date(verificationFlows.email.expiresAt).getTime() : false;
  return <AccountPage title="Profile" description="Manage the identity Cascade uses across your DemoOps workspace.">
    <section className="account-surface-card account-profile-identity"><AccountAvatar profile={profile} large /><div><strong>{profile.displayName}</strong><span>{profile.email || "No email added"}</span></div><button type="button" className="account-profile-logout" onClick={onLogout}>Log out</button></section>
    <form className="account-surface-card account-profile-form" onSubmit={save}>
      <div className="account-card-heading"><div><h2>Personal information</h2><p>These details stay in your local account adapter.</p></div></div>
      <label><span>Username</span><input value={displayName} maxLength={80} onChange={(event) => setDisplayName(event.currentTarget.value)} autoComplete="name" /></label>
      <div className="account-profile-field"><label htmlFor="account-email">Email</label><input id="account-email" value={email} onChange={(event) => setEmail(event.currentTarget.value)} type="email" autoComplete="email" /><VerificationControl channel="email" status={email.trim() === (profile.email ?? "") ? profile.emailVerification : "unverified"} disabled={!email.trim() || verificationBusy !== undefined} hasFlow={verificationFlows.email !== undefined} resendSeconds={emailResendSeconds} onStart={() => void beginVerification("email")} /></div>
      {verificationFlows.email ? <VerificationCodePanel channel="email" flow={verificationFlows.email} value={verificationCodes.email ?? ""} busy={verificationBusy === "email"} expired={emailExpired} onChange={(value) => setVerificationCodes((current) => ({ ...current, email: value }))} onConfirm={() => void confirmVerification("email")} /> : null}
      <div className="account-form-footer"><div>{error ? <span className="account-inline-error" role="alert">{error}</span> : message ? <span className="account-inline-success">{message}</span> : null}</div><button type="submit" className="account-primary-button" disabled={saving || !dirty || !displayName.trim()}>{saving ? "Saving…" : "Save changes"}</button></div>
    </form>
    <section className="account-surface-card account-password-card">
      <div className="account-card-heading"><div><h2>Password</h2><p>{profile.hasPassword ? "••••••••••••" : "Not set"}</p></div><button type="button" className="account-secondary-button" onClick={() => setPasswordOpen((value) => !value)}>{passwordOpen ? "Cancel" : profile.hasPassword ? "Edit" : "Set password"}</button></div>
      {passwordOpen ? <form className="account-password-form" onSubmit={savePassword}>
        {profile.hasPassword ? <label><span>Current password</span><input required type="password" autoComplete="current-password" value={currentPassword} onChange={(event) => setCurrentPassword(event.currentTarget.value)} /></label> : null}
        <label><span>New password</span><input required type="password" minLength={12} maxLength={128} autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.currentTarget.value)} /><small>12–128 characters</small></label>
        <label><span>Confirm new password</span><input required type="password" minLength={12} maxLength={128} autoComplete="new-password" value={confirmPassword} onChange={(event) => setConfirmPassword(event.currentTarget.value)} /></label>
        <p>Updating your password signs you out on this device.</p>
        <button type="submit" className="account-primary-button" disabled={passwordBusy || newPassword.length < 12 || confirmPassword.length < 12}>{passwordBusy ? "Saving…" : profile.hasPassword ? "Change password" : "Set password"}</button>
      </form> : null}
    </section>
    <section className="account-surface-card">
      <div className="account-card-heading"><div><h2>GitHub account</h2><p>Connect your identity separately from repository access tokens.</p></div>{profile.github ? <span className="account-connected-badge">Connected</span> : null}</div>
      {profile.github ? <div className="github-identity-row">
        <span className="github-avatar">{profile.github.avatarURL ? <img src={profile.github.avatarURL} alt="" /> : "GH"}</span>
        <div><strong>{profile.github.name || profile.github.login}</strong><span>@{profile.github.login}</span></div>
        {confirmDisconnect ? <div className="github-confirm-actions"><span>Disconnect this account?</span><button type="button" onClick={() => setConfirmDisconnect(false)}>Cancel</button><button type="button" className="danger" disabled={githubBusy} onClick={disconnectGitHub}>Disconnect</button></div> : <button type="button" className="account-secondary-button" onClick={() => setConfirmDisconnect(true)}>Disconnect</button>}
      </div> : flow?.status === "pending" ? <div className="github-device-flow">
        <div><span>Enter this code on GitHub</span><strong>{flow.userCode}</strong><small>Expires {new Date(flow.expiresAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}</small></div>
        <a className="account-primary-button" href={flow.verificationURI} target="_blank" rel="noreferrer">Open GitHub</a>
        <p><span className="account-spinner" />Waiting for authorization…</p>
      </div> : <div className="github-empty-row"><div><strong>No GitHub account connected</strong><span>Cascade will only request basic profile access.</span></div><button type="button" className="account-secondary-button" disabled={githubBusy} onClick={connectGitHub}>{githubBusy ? "Starting…" : "Connect GitHub"}</button></div>}
      {flow && ["denied", "expired", "failed"].includes(flow.status) ? <div className="account-inline-error" role="alert">{flow.error || `GitHub authorization ${flow.status}.`}</div> : null}
    </section>
  </AccountPage>;
}

export function PlanPanel({ plan, error, onRetry }: { plan?: AccountPlanView; error: string; onRetry: () => void }) {
  if (!plan) return <AccountPage title="Plan" description="Your Cascade usage plan and current credit cycle."><div className="account-surface-card account-empty-state">{error ? <><strong>Plan unavailable</strong><p>{error}</p><button type="button" className="account-secondary-button" onClick={onRetry}>Try again</button></> : <><span className="account-spinner" /><p>Loading plan…</p></>}</div></AccountPage>;
  const percent = plan.creditsIncluded > 0 ? Math.min(100, Math.round((plan.creditsUsed / plan.creditsIncluded) * 100)) : 0;
  const categoryLabels = { research: "Research & understanding", browser: "Browser execution", video: "Video processing" };
  return <AccountPage title="Plan" description="One credit balance across research, browser execution, and video work.">
    <section className="account-plan-hero">
      <div><span>Current plan</span><h2>{plan.name}</h2><small className={`plan-status ${plan.status}`}>{plan.status.replace("_", " ")}</small></div>
      <div className="plan-remaining"><strong>{plan.creditsRemaining.toLocaleString()}</strong><span>credits remaining</span></div>
    </section>
    <section className="account-surface-card plan-usage-card">
      <div className="account-card-heading"><div><h2>Usage this cycle</h2><p>{formatAccountDate(plan.cycleStart)} – {formatAccountDate(plan.cycleEnd)}</p></div><strong>{plan.creditsUsed.toLocaleString()} / {plan.creditsIncluded.toLocaleString()}</strong></div>
      <div className="plan-progress"><span style={{ width: `${percent}%` }} /></div>
      <div className="plan-usage-list">{plan.usage.map((item) => <div key={item.category}><span>{categoryLabels[item.category]}</span><strong>{item.credits.toLocaleString()} credits</strong></div>)}</div>
    </section>
    <p className="account-page-note">Usage is supplied by the local development account adapter. Billing and purchasing are not enabled in this build.</p>
  </AccountPage>;
}

export function AccountPage({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return <div className="account-page"><header><span className="account-page-eyebrow">Account</span><h1>{title}</h1><p>{description}</p></header><div className="account-page-content">{children}</div></div>;
}

function VerificationBadge({ status }: { status: AccountProfileView["emailVerification"] }) {
  return <small className={`verification-badge ${status}`}>{status}</small>;
}

function VerificationControl({ channel, status, disabled, hasFlow, resendSeconds, onStart }: { channel: AccountVerificationChannel; status: AccountProfileView["emailVerification"]; disabled: boolean; hasFlow: boolean; resendSeconds: number; onStart: () => void }) {
  const actionLabel = hasFlow ? resendSeconds > 0 ? `Resend in ${resendSeconds}s` : "Resend" : `Verify ${channel}`;
  return <div className="verification-control"><VerificationBadge status={status} />{status !== "verified" ? <button type="button" disabled={disabled || hasFlow && resendSeconds > 0} onClick={onStart}>{actionLabel}</button> : null}</div>;
}

function VerificationCodePanel({ channel, flow, value, busy, expired, onChange, onConfirm }: { channel: AccountVerificationChannel; flow: AccountVerificationStartView; value: string; busy: boolean; expired: boolean; onChange: (value: string) => void; onConfirm: () => void }) {
  return <div className="account-verification-panel">
    <div><strong>{expired ? "Code expired" : "Enter the 6-digit code"}</strong><span>{expired ? "Request a new code to continue." : `Sent to ${flow.maskedDestination} · expires ${new Date(flow.expiresAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`}</span>{flow.developmentCode ? <small>Development code: <code>{flow.developmentCode}</code></small> : null}</div>
    <input aria-label={`${channel} verification code`} value={value} disabled={expired} onChange={(event) => onChange(event.currentTarget.value.replace(/\D/g, "").slice(0, 6))} inputMode="numeric" autoComplete="one-time-code" placeholder="000000" />
    <button type="button" className="account-primary-button" disabled={busy || expired || value.length !== 6} onClick={onConfirm}>{busy ? "Verifying…" : "Confirm"}</button>
  </div>;
}

function verificationErrorMessage(code: string | undefined, fallback: string | undefined) {
  const labels: Record<string, string> = {
    verification_invalid: "That verification code is incorrect. Try again.",
    verification_expired: "That verification code has expired. Request a new code.",
    verification_attempts_exhausted: "Too many incorrect attempts. Request a new code.",
    verification_rate_limited: fallback || "Please wait before requesting another code.",
    verification_not_configured: "Email verification is not configured on this device.",
    verification_sender_invalid: "The verification email sender is misconfigured.",
    verification_delivery_config: fallback || "The email provider rejected the delivery configuration.",
    verification_delivery_failed: fallback || "The verification message could not be delivered. Try again.",
    verification_destination_unsaved: "Save this email before verifying it.",
  };
  return code && labels[code] ? labels[code] : fallback || "Email verification failed.";
}

function formatAccountDate(value: string) {
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" }).format(new Date(value));
}
