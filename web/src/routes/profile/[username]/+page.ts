// The profile fetch is client-owned. Keeping SSR off avoids issuing the same
// expensive public analytics query once from Node and again during hydration.
export const ssr = false;
