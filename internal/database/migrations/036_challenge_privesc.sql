-- privesc: opt-in relaxation of the container securityContext
-- (allowPrivilegeEscalation=true / no_new_privs off) for boot-to-root and
-- SUID-based privilege-escalation challenges. Capabilities stay dropped-ALL and
-- gVisor + the default-deny NetworkPolicies are untouched — only the setuid
-- transition is re-enabled. Default off; only opt-in challenges relax.
ALTER TABLE challenges ADD COLUMN IF NOT EXISTS privesc BOOLEAN NOT NULL DEFAULT false;
