/**
 * Markdown rendering for the agent chat, sanitized: answers quote establishment
 * names and scan evidence, so nothing from a model message may reach the DOM
 * unescaped. Links always open in a new tab — they point at external sources
 * (Google Maps listings, official data portals) and leaving the report to
 * visit one is never what you want.
 */
import { marked } from 'marked';
import DOMPurify from 'dompurify';

let hooked = false;

function ensureHook(): void {
	if (hooked || typeof window === 'undefined') return;
	// The canonical DOMPurify recipe: rewrite anchors after sanitization.
	DOMPurify.addHook('afterSanitizeAttributes', (node) => {
		if (node.tagName === 'A') {
			node.setAttribute('target', '_blank');
			node.setAttribute('rel', 'noopener noreferrer');
		}
	});
	hooked = true;
}

export function renderMarkdown(text: string): string {
	ensureHook();
	return DOMPurify.sanitize(marked.parse(text, { async: false }) as string, {
		ADD_ATTR: ['target', 'rel']
	});
}
