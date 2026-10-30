import { useEffect, useId, useRef, useState } from 'react';

type Props = {
  title: string;
  label: string;
  description?: string;
  type?: 'text' | 'date';
  placeholder?: string;
  onClose: () => void;
  onSubmit: (value: string) => string | undefined;
};

export function SimulationInputModal({ title, label, description, type = 'text', placeholder, onClose, onSubmit }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const titleId = useId(), inputId = useId(), errorId = useId();
  const [value, setValue] = useState('');
  const [error, setError] = useState('');

  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null;
    const element = dialog.current!;
    element.showModal();
    input.current?.focus();
    return () => { element.close(); previousFocus?.focus(); };
  }, []);

  return <dialog ref={dialog} aria-labelledby={titleId}
    onCancel={event => { event.preventDefault(); onClose(); }}
    onClick={event => {
      const bounds = event.currentTarget.getBoundingClientRect();
      if (event.target === event.currentTarget && (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom)) onClose();
    }}
    className="m-auto w-[calc(100%-2rem)] max-w-lg rounded-xl border-0 bg-white p-0 text-gray-900 shadow-2xl backdrop:bg-black/40">
    <div className="flex items-center justify-between border-b border-gray-100 px-6 py-4">
      <h2 id={titleId} className="text-base font-semibold">{title}</h2>
      <button type="button" aria-label="Close dialog" onClick={onClose} className="text-xl leading-none text-gray-400 hover:text-gray-600">&times;</button>
    </div>
    <form className="space-y-4 px-6 py-4" onSubmit={event => {
      event.preventDefault();
      const trimmed = value.trim();
      if (!trimmed) { setError(`${label} is required.`); return; }
      const message = onSubmit(trimmed);
      if (message) { setError(message); return; }
      onClose();
    }}>
      {description && <p className="text-sm text-gray-500">{description}</p>}
      <div>
        <label htmlFor={inputId} className="mb-1 block text-xs font-medium text-gray-600">{label}</label>
        <input ref={input} id={inputId} required type={type} value={value} placeholder={placeholder}
          aria-invalid={!!error} aria-describedby={error ? errorId : undefined}
          onChange={event => { setValue(event.target.value); setError(''); }} className="input" />
      </div>
      {error && <p id={errorId} role="alert" className="text-sm text-red-700">{error}</p>}
      <div className="flex justify-end gap-2 pt-2">
        <button type="button" onClick={onClose} className="rounded-lg border border-gray-200 px-4 py-2 text-sm">Cancel</button>
        <button type="submit" className="rounded-lg bg-blue-600 px-4 py-2 text-sm text-white">Add</button>
      </div>
    </form>
  </dialog>;
}
