import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { AccountPlanView, AccountProfileView, NavSection } from "./domain";

export function AccountMenu({ profile, plan, onNavigate, onLogout }: { profile: AccountProfileView; plan?: AccountPlanView; onNavigate: (section: Extract<NavSection, "profile" | "settings" | "plan">) => void; onLogout: () => void }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const firstItemRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    firstItemRef.current?.focus();
    const onPointerDown = (event: globalThis.MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) close(false);
    };
    document.addEventListener("mousedown", onPointerDown);
    return () => document.removeEventListener("mousedown", onPointerDown);
  }, [open]);

  function close(restoreFocus = true) {
    setOpen(false);
    if (restoreFocus) window.setTimeout(() => triggerRef.current?.focus(), 0);
  }

  function choose(section: Extract<NavSection, "profile" | "settings" | "plan">) {
    close(false);
    onNavigate(section);
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      close();
    }
  }

  return <div className="account-menu-root" ref={rootRef} onKeyDown={handleKeyDown}>
    {open ? <div className="account-popover" role="menu" aria-label="Account menu">
      <div className="account-popover-profile">
        <AccountAvatar profile={profile} />
        <div><strong>{profile.displayName}</strong><span>{profile.email || "Local Cascade account"}</span></div>
      </div>
      <div className="account-menu-divider" />
      <button ref={firstItemRef} type="button" role="menuitem" onClick={() => choose("profile")}><AccountIcon kind="profile" /><span>Profile</span></button>
      <button type="button" role="menuitem" onClick={() => choose("settings")}><AccountIcon kind="settings" /><span>Settings</span><small>⌘,</small></button>
      <button type="button" role="menuitem" onClick={() => choose("plan")}><AccountIcon kind="plan" /><span>Plan</span><small>{plan ? `${plan.creditsRemaining} left` : "Credits"}</small></button>
      <div className="account-menu-divider" />
      <button type="button" role="menuitem" className="account-logout-item" onClick={() => { close(false); onLogout(); }}><AccountIcon kind="logout" /><span>Log out</span></button>
    </div> : null}
    <button ref={triggerRef} type="button" className="account-trigger" aria-label={`Open account menu for ${profile.displayName}`} aria-expanded={open} onClick={() => setOpen((value) => !value)}>
      <AccountAvatar profile={profile} />
      <strong>{profile.displayName}</strong>
      <span className="account-trigger-chevron" aria-hidden="true">⌃</span>
    </button>
  </div>;
}

export function AccountAvatar({ profile, large = false }: { profile: Pick<AccountProfileView, "displayName" | "initials" | "avatarURL">; large?: boolean }) {
  return <span className={`account-avatar ${large ? "large" : ""}`} aria-hidden="true">
    {profile.avatarURL ? <img src={profile.avatarURL} alt="" /> : profile.initials || profile.displayName.slice(0, 2).toUpperCase()}
  </span>;
}

function AccountIcon({ kind }: { kind: "profile" | "settings" | "plan" | "logout" }) {
  const paths = {
    profile: <><circle cx="12" cy="8" r="3.2"/><path d="M5.5 19c.8-3.4 3-5.1 6.5-5.1s5.7 1.7 6.5 5.1"/></>,
    settings: <><circle cx="12" cy="12" r="3"/><path d="M19 13.5v-3l-2-.7-.7-1.7.9-1.9-2.1-2.1-1.9.9-1.7-.7-.7-2h-3l-.7 2-1.7.7-1.9-.9-2.1 2.1.9 1.9-.7 1.7-2 .7v3l2 .7.7 1.7-.9 1.9 2.1 2.1 1.9-.9 1.7.7.7 2h3l.7-2 1.7-.7 1.9.9 2.1-2.1-.9-1.9.7-1.7z"/></>,
    plan: <><path d="M4 7.5h16M7 3.5v4M17 3.5v4M5 5.5h14a1 1 0 0 1 1 1v13H4v-13a1 1 0 0 1 1-1z"/><path d="m8 14 2.1 2 5-5"/></>,
    logout: <><path d="M10 5H5v14h5M14 8l4 4-4 4M8 12h10"/></>,
  };
  return <svg className="account-menu-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[kind]}</svg>;
}
