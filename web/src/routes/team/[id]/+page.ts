// The team profile fetch is client-owned. Keeping SSR off avoids issuing the
// same analytics query once from Node and again during hydration.
export const ssr = false;
