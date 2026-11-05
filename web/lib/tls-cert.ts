import type { TLSCertInfo } from './types';

/**
 * The expiry to show for a recorded certificate: the chain's earliest when
 * the monitor opted into tls_check_chain and the snapshot carries one, else
 * the leaf's. The TS twin of Go shared/tlscert Info.Expiry, which the
 * alerter's tls_expiry and the status page use.
 */
export function effectiveCertExpiry(
  tls: TLSCertInfo,
  checkChain = false
): { notAfter?: string; days?: number; chain: boolean } {
  if (checkChain && tls.chain_not_after) {
    return { notAfter: tls.chain_not_after, days: tls.chain_min_days, chain: true };
  }
  return { notAfter: tls.not_after, days: tls.days_until_expiry, chain: false };
}
