import { type Dispatch, type SetStateAction } from 'react';
import { initialDatabaseFormState } from '@/lib/monitor-form/database';

/** DatabaseForm's state, as its panels read and edit it. */
export type DatabaseFormData = ReturnType<typeof initialDatabaseFormState>;
export type SetDatabaseFormData = Dispatch<SetStateAction<DatabaseFormData>>;

/** Stored secrets the user dropped with Clear: submitted as "" instead of "***". */
export interface ClearedDatabaseSecrets {
  password: boolean;
  clientKey: boolean;
}
