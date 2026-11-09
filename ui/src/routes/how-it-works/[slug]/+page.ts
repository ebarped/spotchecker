import { PIPELINE_DETAILS, type PhaseDetail } from '../../../lib/pipeline';
import type { PageLoad } from './$types';

export const prerender = true;
export const entries = () => PIPELINE_DETAILS.map((phase: PhaseDetail) => ({ slug: phase.slug }));

export const load: PageLoad = ({ params }) => ({ slug: params.slug });
