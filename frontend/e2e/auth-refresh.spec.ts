import { test, expect } from '@playwright/test';

// Exercise the real proxy/backend contract without recording token-bearing traces.
test.use({ trace: 'off' });

for (const mode of ['expiry', 'rejected-token', 'legacy-storage'] as const) {
  test(`session survives ${mode} and refreshes again after reload`, async ({ page, context }) => {
    const login = await context.request.post('/auth/login', { data: {
      email: process.env.SUPER_USER_EMAIL ?? 'admin@goflow.com',
      password: process.env.SUPER_USER_PASSWORD ?? 'admin123',
    } });
    expect(login.status()).toBe(200);
    const tokens = await login.json();
    // Expiry/rejection must work from the request body alone. Legacy broken
    // storage instead exercises the backend's HTTP-only cookie fallback.
    if (mode !== 'legacy-storage') await context.clearCookies();
    await page.addInitScript(({tokens, mode}) => {
      if (sessionStorage.getItem('auth-refresh-test-initialized')) return;
      sessionStorage.setItem('auth-refresh-test-initialized','true');
      const expired = `header.${btoa(JSON.stringify({exp:1}))}.signature`;
      localStorage.setItem('accessToken', mode === 'expiry' ? expired : mode === 'legacy-storage' ? 'undefined' : 'rejected-access-token');
      localStorage.setItem('refreshToken', mode === 'legacy-storage' ? 'undefined' : tokens.refresh_token);
    }, { tokens, mode });
    const refreshStatuses: number[] = [], errors: string[] = [];
    page.on('response', response => { if (response.url().endsWith('/auth/refresh')) refreshStatuses.push(response.status()); });
    page.on('pageerror', error => errors.push(error.message));
    await page.goto('/simulation');
    await expect(page.getByRole('heading',{name:'Simulation studio',exact:true})).toBeVisible();
    await expect.poll(() => refreshStatuses).toEqual([200]);
    expect(await page.evaluate(() => ['accessToken','refreshToken'].every(key => {
      const token=localStorage.getItem(key);return !!token && token !== 'undefined' && token.split('.').length===3;
    }))).toBe(true);
    // Verify that the rotated refresh token remains usable in a later cycle.
    await page.evaluate(() => localStorage.setItem('accessToken', `header.${btoa(JSON.stringify({exp:1}))}.signature`));
    await page.reload();
    await expect(page.getByRole('heading',{name:'Simulation studio',exact:true})).toBeVisible();
    await expect.poll(() => refreshStatuses).toEqual([200,200]);
    await expect(page).toHaveURL(/\/simulation$/);
    expect(errors).toEqual([]);
  });
}
