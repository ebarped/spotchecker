// Pure SPA: the scan report is fetched from /api/report in the browser, so
// nothing renders on the server. adapter-static prerenders the shell and the
// app hydrates client-side.
export const ssr = false;
export const prerender = true;
