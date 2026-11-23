(() => {
  'use strict';
  const root = document.getElementById('accounts-page');
  if (!root) return;
  const el = name => document.getElementById('accounts-' + name);
  const base = (document.querySelector('meta[name="base-path"]')?.content || '').replace(/\/$/, '');
  let accounts = [], providers = [], selected = null, editing = null;
  let busy = false, usageController = null, usageGeneration = 0;
  const text = (name, value) => { el(name).textContent = value == null ? '' : String(value); };
  const error = (name, value) => { text(name, value); el(name).hidden = !value; };
  const current = () => accounts.find(a => String(a.id) === String(selected));
  const providerLabel = id => providers.find(p => p.id === id)?.label || id;
  const number = value => typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString() : 'Unknown';
  function money(value, currency = 'USD') {
    if (typeof value !== 'number' || !Number.isFinite(value)) return 'Unknown';
    try { return new Intl.NumberFormat(undefined, { style: 'currency', currency, maximumFractionDigits: 6 }).format(value); }
    catch (_) { return value.toLocaleString() + ' ' + currency; }
  }
  function date(value) {
    if (!value) return 'Unknown';
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
  }
  async function api(path, options = {}) {
    const headers = { Accept: 'application/json', ...options.headers };
    if (options.method && options.method !== 'GET') {
      headers['X-Requested-With'] = 'XMLHttpRequest';
      if (options.body) headers['Content-Type'] = 'application/json';
    }
    const response = await fetch(base + path, { ...options, headers, credentials: 'same-origin', cache: 'no-store' });
    if (response.status === 401) {
      window.location.assign(base + '/login');
      throw new Error('Session expired. Please sign in again.');
    }
    let data;
    try { data = await response.json(); }
    catch (_) { throw new Error('The server returned an unreadable response (HTTP ' + response.status + ').'); }
    if (!response.ok || data.success === false) throw new Error(data.error || data.message || 'Request failed (HTTP ' + response.status + ').');
    return data;
  }
  function setBusy(value) {
    busy = value;
    ['add', 'save', 'cancel', 'edit', 'pause', 'delete'].forEach(name => { el(name).disabled = value || (name === 'add' && !providers.length); });
    el('discover').disabled = value || el('discover').dataset.available !== 'true';
    el('list').querySelectorAll('button').forEach(button => { button.disabled = value; });
  }
  function renderList() {
    el('list').replaceChildren();
    if (!accounts.length) {
      const p = document.createElement('p');
      p.textContent = 'No accounts yet. Add an account or discover optional omp credentials.';
      el('list').append(p);
    }
    accounts.forEach(account => {
      const button = document.createElement('button');
      button.type = 'button';
      button.setAttribute('aria-pressed', String(String(account.id) === String(selected)));
      button.textContent = account.name || providerLabel(account.provider);
      const meta = document.createElement('span');
      meta.textContent = [providerLabel(account.provider), account.enabled ? 'Enabled' : 'Paused', account.mode === 'cost' ? 'Costs' : 'Quota', account.source || 'manual'].join(' · ');
      button.append(meta);
      button.disabled = busy;
      button.addEventListener('click', () => selectAccount(account.id));
      el('list').append(button);
    });
  }
  function invalidateUsage() {
    usageGeneration++;
    usageController?.abort();
    usageController = null;
  }
  function selectAccount(id) {
    invalidateUsage();
    selected = id;
    const account = current();
    renderList();
    el('selected').hidden = !account;
    el('empty').hidden = !!account;
    el('usage').hidden = true;
    error('usage-error', '');
    el('detail').setAttribute('aria-busy', 'false');
    text('detail-title', account?.name || 'Account usage');
    if (!account) return;
    text('selected-meta', [providerLabel(account.provider), account.source || 'manual', account.enabled ? 'Enabled' : 'Paused'].join(' · '));
    text('pause', account.enabled ? 'Pause' : 'Resume');
    el('quota-link').href = base + '/?provider=' + encodeURIComponent(account.provider) + '&account=' + encodeURIComponent(account.id);
    el('quota-link').hidden = account.mode === 'cost';
    const url = new URL(window.location.href);
    url.searchParams.set('account', account.id);
    window.history.replaceState(null, '', url);
    loadUsage();
  }
  async function loadAccounts(preferred = selected) {
    const data = await api('/api/accounts');
    accounts = Array.isArray(data.accounts) ? data.accounts : [];
    providers = Array.isArray(data.providers) ? data.providers : [];
    el('provider').replaceChildren();
    providers.forEach(provider => {
      const option = document.createElement('option');
      option.value = provider.id;
      option.textContent = provider.label || provider.id;
      el('provider').append(option);
    });
    const discovery = data.discovery || {};
    el('discover').dataset.available = String(!!(discovery.enabled && discovery.available));
    text('discovery-note', discovery.message || (discovery.enabled && discovery.available ? 'Optional: discover credentials from omp. Cost estimation does not require omp.' : 'Optional omp discovery is unavailable. Add accounts manually; costs do not require omp.'));
    setBusy(busy);
    selectAccount(accounts.find(a => String(a.id) === String(preferred))?.id ?? accounts[0]?.id ?? null);
  }
  function credentialFields() {
    const account = editing;
    const readOnly = !!account && (account.readOnly || account.source === 'omp');
    const fields = providers.find(p => p.id === el('provider').value)?.fields || [];
    el('credential-fields').replaceChildren();
    text('credential-note', readOnly ? 'Discovered credentials are read-only and managed by omp. No stored credentials are displayed.' : account ? 'Stored credentials are never displayed. Leave credential fields blank to preserve existing values.' : 'Credentials are stored by onWatch and are never displayed after saving.');
    fields.forEach((field, index) => {
      const label = document.createElement('label');
      label.textContent = field.label || field.key;
      const input = document.createElement('input');
      input.id = 'accounts-credential-' + index;
      input.type = 'password';
      input.autocomplete = 'new-password';
      input.spellcheck = false;
      input.dataset.field = field.key;
      input.required = !!field.required && !readOnly && (!account || !account.hasCredential);
      input.disabled = readOnly;
      input.placeholder = readOnly ? 'Managed by omp' : account?.hasCredential ? 'Unchanged unless entered' : '';
      label.append(input);
      el('credential-fields').append(label);
    });
  }
  function clearCredentials() { el('credential-fields').querySelectorAll('input').forEach(input => { input.value = ''; }); }
  function closeEditor() {
    clearCredentials();
    editing = null;
    el('form').reset();
    el('editor').hidden = true;
    error('form-error', '');
  }
  function openEditor(account) {
    closeEditor();
    editing = account || null;
    text('editor-title', account ? 'Edit account' : 'Add account');
    el('provider').disabled = !!account;
    el('provider').value = account?.provider || providers[0]?.id || '';
    // Legacy providers absent from the catalog still need editable account settings.
    if (account && !providers.some(p => p.id === account.provider)) {
      const option = document.createElement('option');
      option.value = account.provider;
      option.textContent = providerLabel(account.provider);
      el('provider').append(option);
      el('provider').value = account.provider;
    }
    el('name').value = account?.name || '';
    el('base-url').value = account?.baseUrl || '';
    el('mode').value = account?.mode || 'quota';
    el('billing').value = account?.billingKind || 'auto';
    el('enabled').checked = account ? !!account.enabled : true;
    credentialFields();
    el('editor').hidden = false;
    el('name').focus();
    el('editor').scrollIntoView({ block: 'nearest' });
  }
  function payload(account) {
    return { id: account.id, provider: account.provider, name: account.name, baseUrl: account.baseUrl || '', mode: account.mode || 'quota', billingKind: account.billingKind || 'auto', enabled: !!account.enabled, credentials: {} };
  }
  async function mutate(action) {
    if (busy) return;
    setBusy(true);
    error('error', '');
    try { await action(); }
    catch (err) { error('error', err.message || 'Account operation failed.'); }
    finally { setBusy(false); }
  }
  function fillTable(name, rows, columns, currency) {
    const body = el(name);
    body.replaceChildren();
    if (!Array.isArray(rows) || !rows.length) {
      const row = document.createElement('tr');
      const cell = document.createElement('td');
      cell.colSpan = columns.length;
      cell.textContent = 'No observed usage for this period.';
      row.append(cell);
      body.append(row);
      return;
    }
    rows.forEach(item => {
      const row = document.createElement('tr');
      columns.forEach(([key, kind]) => {
        const cell = document.createElement('td');
        cell.textContent = kind === 'money' ? money(item[key], currency) : kind === 'number' ? number(item[key]) : String(item[key] ?? 'Unknown');
        row.append(cell);
      });
      body.append(row);
    });
  }
  function renderUsage(data, account) {
    const cost = data.cost || {};
    const currency = cost.currency || 'USD';
    text('cost-amount', money(cost.amount, currency));
    text('cost-source', ({ provider: 'Provider-reported spend', local_estimate: 'Local token estimate · not a provider bill', unavailable: 'Spend unavailable' })[cost.source] || 'Spend source unknown');
    el('cost-facts').replaceChildren();
    [['Scope', cost.scope || 'Unknown — do not combine with other accounts'], ['Period start', date(cost.periodStart)], ['Period end', date(cost.periodEnd)], ['Observed', date(cost.observedAt)], ['Status', cost.status || 'Unknown']].forEach(([label, value]) => {
      const dt = document.createElement('dt'), dd = document.createElement('dd');
      dt.textContent = label;
      dd.textContent = value;
      el('cost-facts').append(dt, dd);
    });
    el('quota-panel').hidden = account.mode === 'cost';
    const hasQuota = data.quota != null && (typeof data.quota !== 'object' || Object.keys(data.quota).length > 0);
    text('quota-empty', hasQuota ? '' : 'Quota data is not available yet. Open the account quota dashboard for provider details.');
    text('quota-data', hasQuota ? JSON.stringify(data.quota, null, 2) : '');
    el('quota-data').hidden = !hasQuota;
    text('usage-message', data.message || '');
    fillTable('daily', data.daily, [['date'], ['amount', 'money'], ['tokens', 'number'], ['requests', 'number']], currency);
    fillTable('models', data.models, [['model'], ['amount', 'money'], ['inputTokens', 'number'], ['outputTokens', 'number'], ['cacheReadTokens', 'number'], ['cacheWriteTokens', 'number'], ['requests', 'number']], currency);
    el('usage').hidden = false;
  }
  async function loadUsage() {
    const account = current();
    if (!account) return;
    invalidateUsage();
    const generation = usageGeneration;
    const controller = new AbortController();
    usageController = controller;
    el('detail').setAttribute('aria-busy', 'true');
    error('usage-error', '');
    try {
      const data = await api('/api/accounts/usage?account=' + encodeURIComponent(account.id) + '&range=' + encodeURIComponent(el('range').value), { signal: controller.signal });
      if (generation !== usageGeneration || String(selected) !== String(account.id)) return;
      if (data.accountId != null && String(data.accountId) !== String(account.id)) throw new Error('Usage response did not match the selected account.');
      renderUsage(data, account);
    } catch (err) {
      if (err.name !== 'AbortError' && generation === usageGeneration) {
        el('usage').hidden = true;
        error('usage-error', err.message || 'Unable to load account usage.');
      }
    } finally {
      if (generation === usageGeneration) { el('detail').setAttribute('aria-busy', 'false'); usageController = null; }
    }
  }
  el('add').addEventListener('click', () => openEditor(null));
  el('edit').addEventListener('click', () => { if (current()) openEditor(current()); });
  el('cancel').addEventListener('click', () => { closeEditor(); el('add').focus(); });
  el('provider').addEventListener('change', credentialFields);
  el('form').addEventListener('submit', async event => {
    event.preventDefault();
    if (busy || !el('form').reportValidity()) return;
    const baseUrl = el('base-url').value.trim();
    if (baseUrl) {
      const url = new URL(baseUrl);
      if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) {
        error('form-error', 'Use an HTTP or HTTPS base URL without embedded credentials.');
        return;
      }
    }
    const body = { provider: el('provider').value, name: el('name').value.trim(), baseUrl, mode: el('mode').value, billingKind: el('billing').value, enabled: el('enabled').checked, credentials: {} };
    if (!body.name) { error('form-error', 'Enter an account name.'); el('name').focus(); return; }
    if (editing) body.id = editing.id;
    el('credential-fields').querySelectorAll('input:not(:disabled)').forEach(input => { if (input.value) body.credentials[input.dataset.field] = input.value; });
    setBusy(true);
    error('form-error', '');
    try {
      const result = await api('/api/accounts', { method: editing ? 'PUT' : 'POST', body: JSON.stringify(body) });
      const id = result.id ?? editing?.id;
      closeEditor();
      text('status', 'Account saved.');
      await loadAccounts(id);
    } catch (err) { error(el('editor').hidden ? 'error' : 'form-error', err.message || 'Unable to save account.'); }
    finally { body.credentials = {}; clearCredentials(); setBusy(false); }
  });
  el('pause').addEventListener('click', () => mutate(async () => {
    const account = current();
    if (!account) return;
    const body = payload(account);
    body.enabled = !body.enabled;
    await api('/api/accounts', { method: 'PUT', body: JSON.stringify(body) });
    text('status', body.enabled ? 'Account resumed.' : 'Account paused. Historical usage is retained.');
    closeEditor();
    await loadAccounts(account.id);
  }));
  el('delete').addEventListener('click', () => {
    const account = current();
    if (!account || !window.confirm('Delete “' + account.name + '”? Collection stops and history is retained.')) return;
    mutate(async () => {
      await api('/api/accounts?id=' + encodeURIComponent(account.id), { method: 'DELETE' });
      closeEditor();
      text('status', 'Account deleted. Historical usage is retained.');
      await loadAccounts(null);
    });
  });
  el('discover').addEventListener('click', () => mutate(async () => {
    const data = await api('/api/accounts/discover', { method: 'POST' });
    closeEditor();
    text('status', (data.message || 'Discovery completed.') + (typeof data.imported === 'number' ? ' Imported: ' + data.imported + '.' : ''));
    await loadAccounts();
  }));
  el('range').addEventListener('change', () => { el('usage').hidden = true; loadUsage(); });
  el('refresh').addEventListener('click', loadUsage);
  document.addEventListener('visibilitychange', () => { if (document.hidden) invalidateUsage(); else loadUsage(); });
  window.addEventListener('pagehide', () => { clearCredentials(); invalidateUsage(); });
  window.setInterval(() => { if (!document.hidden && !busy && !usageController) loadUsage(); }, 60000);
  loadAccounts(new URLSearchParams(window.location.search).get('account')).catch(err => {
    error('error', err.message || 'Unable to load accounts.');
    text('list', 'Accounts could not be loaded. Reload the page to try again.');
  });
})();
