import axios, { AxiosError, AxiosHeaders, type InternalAxiosRequestConfig } from 'axios';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { apiClient } from './client';

const originalAdapter = apiClient.defaults.adapter;
const response = (config: InternalAxiosRequestConfig, data: unknown = {}) => ({config,data,status:200,statusText:'OK',headers:new AxiosHeaders()});
const rejected = (config: InternalAxiosRequestConfig, status = 401) => new AxiosError('Request rejected', 'ERR_BAD_RESPONSE', config, undefined, {...response(config),status});
const expiringToken = () => `header.${btoa(JSON.stringify({exp:Math.floor(Date.now()/1000)-60}))}.signature`;

describe('API session refresh', () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem('accessToken','old-access');
    localStorage.setItem('refreshToken','old-refresh');
    window.history.replaceState({},'', '/login');
  });
  afterEach(() => { apiClient.defaults.adapter=originalAdapter; vi.restoreAllMocks(); });

  it('refreshes expiring tokens before concurrent requests using the backend contract', async () => {
    localStorage.setItem('accessToken',expiringToken());
    let complete!: (value: unknown) => void;
    const refresh=vi.spyOn(axios,'post').mockImplementation(()=>new Promise(resolve=>{complete=resolve;}));
    const send=vi.fn(async (config:InternalAxiosRequestConfig)=>response(config,config.headers.Authorization));
    apiClient.defaults.adapter=send;
    const requests=Promise.all([apiClient.get('/users/me'),apiClient.get('/engine-rest/simulation/scenarios')]);
    await vi.waitFor(()=>expect(refresh).toHaveBeenCalledTimes(1));
    expect(send).not.toHaveBeenCalled();
    expect(refresh).toHaveBeenCalledWith('/auth/refresh',{refresh_token:'old-refresh'},{withCredentials:true,timeout:15000});
    complete({data:{access_token:'new-access',refresh_token:'new-refresh'}});
    expect((await requests).map(r=>r.data)).toEqual(['Bearer new-access','Bearer new-access']);
    expect(localStorage.getItem('refreshToken')).toBe('new-refresh');
  });

  it('shares a refresh after concurrent 401s and reuses it for a delayed old-token rejection', async () => {
    const refresh=vi.spyOn(axios,'post').mockResolvedValue({data:{access_token:'new-access',refresh_token:'new-refresh'}});
    let rejectLate!: (reason: unknown) => void;
    apiClient.defaults.adapter=async config=>{
      if(config.headers.Authorization==='Bearer old-access'){
        if(config.url==='/late')return new Promise((_,reject)=>{rejectLate=()=>reject(rejected(config));});
        throw rejected(config);
      }
      return response(config,config.headers.Authorization);
    };
    const late=apiClient.get('/late');
    const batch=await Promise.all([apiClient.get('/first'),apiClient.get('/second')]);
    expect(batch.map(r=>r.data)).toEqual(['Bearer new-access','Bearer new-access']);
    rejectLate(undefined);
    expect((await late).data).toBe('Bearer new-access');
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('recovers old undefined-token storage through the refresh cookie without storing undefined again', async () => {
    localStorage.setItem('accessToken','undefined');localStorage.setItem('refreshToken','undefined');
    const refresh=vi.spyOn(axios,'post').mockResolvedValue({data:{access_token:'new-access',refresh_token:'new-refresh'}});
    apiClient.defaults.adapter=async config=>{if(!config.headers.Authorization)throw rejected(config);return response(config);};
    await apiClient.get('/users/me');
    expect(refresh).toHaveBeenCalledWith('/auth/refresh',{},expect.objectContaining({withCredentials:true}));
    expect(localStorage.getItem('accessToken')).toBe('new-access');
  });

  it.each([undefined,503])('keeps credentials on a temporary refresh failure (%s)', async status => {
    localStorage.setItem('accessToken',expiringToken());
    const failure=status?rejected({headers:new AxiosHeaders()} as InternalAxiosRequestConfig,status):new AxiosError('Offline','ERR_NETWORK');
    vi.spyOn(axios,'post').mockRejectedValue(failure);
    await expect(apiClient.get('/users/me')).rejects.toBe(failure);
    expect(localStorage.getItem('refreshToken')).toBe('old-refresh');
    expect(localStorage.getItem('accessToken')).not.toBeNull();
  });

  it('clears credentials when the server rejects the refresh token', async () => {
    localStorage.setItem('accessToken',expiringToken());
    vi.spyOn(axios,'post').mockRejectedValue(rejected({url:'/auth/refresh',headers:new AxiosHeaders()} as InternalAxiosRequestConfig));
    await expect(apiClient.get('/users/me')).rejects.toMatchObject({response:{status:401}});
    expect(localStorage.getItem('accessToken')).toBeNull();
    expect(localStorage.getItem('refreshToken')).toBeNull();
  });

  it('never refreshes failed login requests or retries a protected request indefinitely', async () => {
    const refresh=vi.spyOn(axios,'post').mockResolvedValue({data:{access_token:'new-access',refresh_token:'new-refresh'}});
    const send=vi.fn(async(config:InternalAxiosRequestConfig)=>{throw rejected(config);});
    apiClient.defaults.adapter=send;
    await expect(apiClient.post('/auth/login',{})).rejects.toMatchObject({response:{status:401}});
    expect(refresh).not.toHaveBeenCalled();
    send.mockClear();
    await expect(apiClient.get('/protected')).rejects.toMatchObject({response:{status:401}});
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(send).toHaveBeenCalledTimes(2);
  });

  it('does not overwrite a session when refresh returns malformed token data', async () => {
    localStorage.setItem('accessToken',expiringToken());
    vi.spyOn(axios,'post').mockResolvedValue({data:{accessToken:'wrong-contract'}});
    await expect(apiClient.get('/users/me')).rejects.toThrow('Invalid token refresh response');
    expect(localStorage.getItem('refreshToken')).toBe('old-refresh');
  });

  it('does not restore tokens after logout while a refresh is in flight', async () => {
    localStorage.setItem('accessToken',expiringToken());
    let complete!: (value:unknown)=>void;
    const refresh=vi.spyOn(axios,'post').mockImplementation(()=>new Promise(resolve=>{complete=resolve;}));
    const request=apiClient.get('/users/me');
    const assertion=expect(request).rejects.toThrow('Session changed during token refresh');
    await vi.waitFor(()=>expect(refresh).toHaveBeenCalledTimes(1));
    localStorage.clear();
    complete({data:{access_token:'new-access',refresh_token:'new-refresh'}});
    await assertion;
    expect(localStorage.getItem('accessToken')).toBeNull();
  });
});
