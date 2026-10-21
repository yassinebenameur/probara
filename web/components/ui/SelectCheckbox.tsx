'use client';

import Checkbox from './Checkbox';

interface SelectCheckboxProps {
  checked: boolean;
  indeterminate?: boolean;
  onChange: () => void;
  disabled?: boolean;
  /** Accessible name ("Select Orders API"). */
  label?: string;
  size?: 'sm' | 'md';
  className?: string;
}

/**
 * Row-selection box for lists and pickers: a bare Checkbox that keeps its
 * clicks from reaching the row (rows are links or toggle on click).
 */
export default function SelectCheckbox({
  checked,
  indeterminate = false,
  onChange,
  disabled = false,
  label,
  size = 'md',
  className = '',
}: SelectCheckboxProps) {
  return (
    <Checkbox
      checked={checked}
      indeterminate={indeterminate}
      onChange={() => onChange()}
      disabled={disabled}
      aria-label={label}
      size={size}
      stopPropagation
      className={className}
    />
  );
}
