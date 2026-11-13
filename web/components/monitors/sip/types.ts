import { type Dispatch, type SetStateAction } from 'react';
import { initialSipFormState } from '@/lib/monitor-form/sip';

/** SipForm's state, as its panels read and edit it. */
export type SipFormData = ReturnType<typeof initialSipFormState>;
export type SetSipFormData = Dispatch<SetStateAction<SipFormData>>;

// Error keys that live in the call's Advanced disclosure, opened on submit
// when one of them fails.
export const CALL_ADVANCED_FIELDS = ['call_target_uri', 'call_sdp_address'];
