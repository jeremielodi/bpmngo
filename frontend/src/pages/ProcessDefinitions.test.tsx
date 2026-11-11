import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import ProcessDefinitions from './ProcessDefinitions';
import * as processesApi from '../api/processes';
import type { ProcessDefinition } from '../types';

vi.mock('../api/processes');
vi.mock('../hooks/useAuth', () => ({ useAuth: () => ({ hasAction: () => false }) }));

const def = (key: string, name: string): ProcessDefinition => ({
  id: `${key}-id`,
  key,
  name,
  version: 1,
  deploymentId: 'dep-1',
  createdAt: '2026-11-01T10:00:00Z',
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <ProcessDefinitions />
      </MemoryRouter>
    </QueryClientProvider>
  );
}

describe('ProcessDefinitions page', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(processesApi.listDefinitions).mockResolvedValue([
      def('order-process', 'Order handling'),
      def('invoice-approval', 'Invoice approval'),
    ]);
  });

  it('filters the list by name or key', async () => {
    renderPage();
    await screen.findByText('Order handling');

    const search = screen.getByLabelText('Search process definitions');
    await userEvent.type(search, 'INVOICE');
    expect(screen.queryByText('Order handling')).not.toBeInTheDocument();
    expect(screen.getByText('Invoice approval')).toBeInTheDocument();

    await userEvent.clear(search);
    await userEvent.type(search, 'order-proc');
    expect(screen.getByText('Order handling')).toBeInTheDocument();
    expect(screen.queryByText('Invoice approval')).not.toBeInTheDocument();

    await userEvent.clear(search);
    await userEvent.type(search, 'nothing-like-this');
    expect(screen.getByText('No matching definitions')).toBeInTheDocument();
  });

  it('shows the server message when a deployment fails', async () => {
    vi.mocked(processesApi.deployBpmn).mockRejectedValue({
      response: { status: 400, data: { error: 'invalid resource: bad.bpmn: XML syntax error on line 3' } },
    });
    const { container } = renderPage();
    await screen.findByText('Order handling');

    await userEvent.click(screen.getByRole('button', { name: /Deploy BPMN/ }));
    const input = container.ownerDocument.querySelector('input[type="file"]') as HTMLInputElement;
    await userEvent.upload(input, new File(['<bpmn'], 'bad.bpmn', { type: 'application/xml' }));

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'invalid resource: bad.bpmn: XML syntax error on line 3'
    );
  });
});
