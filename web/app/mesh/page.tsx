import { redirect } from 'next/navigation';

// The mesh matrix is a tab of the Locations page. Kept as a route because
// mesh-edge alert notifications deep-link here (shared/notifications/present).
export default function MeshPage() {
  redirect('/locations?tab=mesh');
}
