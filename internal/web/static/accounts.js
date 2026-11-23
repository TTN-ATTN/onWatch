(() => {
  'use strict';
  const root = document.getElementById('accounts-page');
  if (!root) return;
  const el = name => document.getElementById('accounts-' + name);
  const base = (document.querySelector('meta[name="base-path"]')?.content || '').replace(/\/$/, '');
  let accounts = [], providers = [], selected = null, editing = null;
  let busy = false, usageController = null, usageGeneration = 0, toastTimer = 0;
  const text = (name, value) => { el(name).textContent = value == null ? '' : String(value); };
  const error = (name, value) => { text(name, value); el(name).hidden = !value; };
  const current = () => accounts.find(a => String(a.id) === String(selected));
  const providerLabel = id => providers.find(p => p.id === id)?.label || id;
  const isNumber = value => typeof value === 'number' && Number.isFinite(value);
  const number = value => isNumber(value) ? value.toLocaleString() : 'Unknown';
  const compact = value => isNumber(value) ? new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(value) : 'Unknown';
  const billingLabel = kind => [...el('billing').options].find(option => option.value === kind)?.textContent || kind || 'Automatic';
  const rangeLabel = () => el('range').selectedOptions[0]?.textContent.toLowerCase() || '';
  const SOURCES = { omp: 'omp (read-only; edit it in omp)', manual: 'Added in onWatch', onwatch: 'onWatch configuration' };
  const MODE_NOTES = {
    quota: 'Quota dashboard polls the provider\'s quota or balance API, like the main dashboard.',
    cost: 'Costs & usage reads spend from the billing adapter, or estimates it from calls recorded through API Integrations.',
  };
  function money(value, currency = 'USD') {
    if (!isNumber(value)) return 'Unknown';
    try { return new Intl.NumberFormat(undefined, { style: 'currency', currency, maximumFractionDigits: 6 }).format(value); }
    catch (_) { return value.toLocaleString() + ' ' + currency; }
  }
  function date(value) {
    if (!value) return 'Unknown';
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
  }
  function host(url) {
    try { return url ? new URL(url).host : ''; } catch (_) { return ''; }
  }
  function make(tag, className, content) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (content != null) node.textContent = String(content);
    return node;
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
  function toast(message, isError = false) {
    window.clearTimeout(toastTimer);
    text('status', message);
    el('status').classList.toggle('is-error', isError);
    el('status').classList.toggle('is-visible', !!message);
    if (message) toastTimer = window.setTimeout(() => el('status').classList.remove('is-visible'), isError ? 7000 : 4000);
  }
  function flash(button, label) {
    const original = button.dataset.label || button.textContent;
    button.dataset.label = original;
    button.textContent = label;
    button.classList.add('is-done');
    window.setTimeout(() => { button.textContent = original; button.classList.remove('is-done'); }, 2000);
  }
  // Safari only honors a clipboard write started inside the click, so a value
  // still being fetched is handed over as a promise instead of awaited first.
  async function writeClipboard(pending) {
    const clipboard = window.isSecureContext ? navigator.clipboard : null;
    if (clipboard?.write && typeof window.ClipboardItem === 'function') {
      try {
        await clipboard.write([new window.ClipboardItem({ 'text/plain': pending.then(value => new Blob([value], { type: 'text/plain' })) })]);
        return;
      } catch (_) { /* Older engines reject promised items; use the fallbacks. */ }
    }
    const value = await pending;
    if (clipboard?.writeText) {
      try { await clipboard.writeText(value); return; } catch (_) { /* Fall back to a selection copy. */ }
    }
    const area = make('textarea', 'accounts-clipboard');
    area.value = value;
    area.readOnly = true;
    document.body.append(area);
    area.select();
    let copied = false;
    try { copied = document.execCommand('copy'); }
    finally { area.value = ''; area.remove(); }
    if (!copied) throw new Error('This browser blocked clipboard access. Open the dashboard over HTTPS or localhost to copy.');
  }
  function setBusy(value) {
    busy = value;
    ['add', 'save', 'cancel', 'edit', 'pause', 'delete'].forEach(name => { el(name).disabled = value || (name === 'add' && !providers.length); });
    el('discover').disabled = value || el('discover').dataset.available !== 'true';
    el('list').querySelectorAll('button').forEach(button => { button.disabled = value; });
  }
  function accountMeta(account) {
    return [account.enabled ? '' : 'Paused', account.mode === 'cost' ? 'Costs' : 'Quota', account.source || 'manual', host(account.baseUrl)].filter(Boolean).join(' · ');
  }
  function renderList() {
    const list = el('list');
    const query = el('filter').value.trim().toLowerCase();
    list.replaceChildren();
    text('count', accounts.length || '');
    if (!accounts.length) {
      list.append(make('p', 'accounts-muted', 'No accounts yet. Add one, or discover omp credentials.'));
      return;
    }
    const matches = accounts.filter(account => !query || [account.name, providerLabel(account.provider), account.provider, account.baseUrl, account.source]
      .some(value => String(value || '').toLowerCase().includes(query)));
    if (!matches.length) {
      list.append(make('p', 'accounts-muted', 'No accounts match “' + el('filter').value.trim() + '”.'));
      return;
    }
    const groups = new Map();
    matches.forEach(account => {
      const label = providerLabel(account.provider);
      if (!groups.has(label)) groups.set(label, []);
      groups.get(label).push(account);
    });
    [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).forEach(([label, items]) => {
      const group = make('div', 'accounts-group');
      const heading = make('h3', 'accounts-group-title');
      heading.append(make('span', '', label), make('span', 'accounts-group-count', items.length));
      group.append(heading);
      items.sort((a, b) => String(a.name).localeCompare(String(b.name), undefined, { numeric: true })).forEach(account => {
        const button = make('button', 'accounts-item');
        button.type = 'button';
        button.setAttribute('aria-pressed', String(String(account.id) === String(selected)));
        const name = make('span', 'accounts-item-name');
        const dot = make('span', 'accounts-dot ' + (account.enabled ? 'is-on' : 'is-off'));
        dot.setAttribute('aria-hidden', 'true');
        name.append(dot, make('span', '', account.name || label));
        button.append(name, make('span', 'accounts-item-meta', accountMeta(account)));
        button.disabled = busy;
        button.addEventListener('click', () => { selectAccount(account.id); revealDetail(); });
        group.append(button);
      });
      list.append(group);
    });
  }
  function invalidateUsage() {
    usageGeneration++;
    usageController?.abort();
    usageController = null;
  }
  // Phones show either the list or one account. Opening an account adds a
  // history entry, so the system back gesture returns to the list.
  const narrow = window.matchMedia('(max-width: 860px)');
  let detailEntry = false;
  const inDetail = () => root.classList.contains('is-detail');
  function showDetail(on) {
    root.classList.toggle('is-detail', on);
    if (!on) detailEntry = false;
  }
  function accountURL(id) {
    const url = new URL(window.location.href);
    url.searchParams.set('account', id);
    return url;
  }
  function revealDetail() {
    const account = current();
    if (!narrow.matches || !account) return;
    if (!inDetail()) {
      showDetail(true);
      window.history.pushState({ accountsDetail: true }, '', accountURL(account.id));
      detailEntry = true;
    }
    window.scrollTo({ top: 0 });
    el('detail-title').focus({ preventScroll: true });
  }
  function focusSelectedItem() { el('list').querySelector('[aria-pressed="true"]')?.focus(); }
  function backToList() {
    const hadEntry = detailEntry;
    showDetail(false);
    if (hadEntry) window.history.back();
    else {
      // Opened from a link: no entry of ours to pop, so drop the deep link instead.
      const url = new URL(window.location.href);
      url.searchParams.delete('account');
      window.history.replaceState(window.history.state, '', url);
    }
    focusSelectedItem();
  }
  function renderBadges(account) {
    el('badges').replaceChildren();
    if (!account) return;
    [
      [providerLabel(account.provider), ''],
      [account.mode === 'cost' ? 'Costs & usage' : 'Quota', ''],
      [account.enabled ? 'Collecting' : 'Paused', account.enabled ? 'is-ok' : 'is-muted'],
      [account.source || 'manual', ''],
    ].forEach(([label, tone]) => el('badges').append(make('span', 'accounts-chip ' + tone, label)));
  }
  function renderConnection(account) {
    text('conn-provider', providerLabel(account.provider));
    text('conn-url', account.baseUrl || 'Provider default endpoint');
    el('conn-url').classList.toggle('is-default', !account.baseUrl);
    el('copy-url').hidden = !account.baseUrl;
    el('conn-key').hidden = !account.hasApiKey;
    el('conn-billing-row').hidden = account.mode !== 'cost';
    text('conn-billing', billingLabel(account.billingKind));
    text('conn-source', SOURCES[account.source] || account.source || 'Added in onWatch');
  }
  function selectAccount(id) {
    invalidateUsage();
    selected = id;
    const account = current();
    const listFocused = el('list').contains(document.activeElement);
    renderList();
    if (listFocused) el('list').querySelector('[aria-pressed="true"]')?.focus();
    el('selected').hidden = !account;
    el('empty').hidden = !!account;
    el('detail-actions').hidden = !account;
    el('usage').hidden = true;
    error('usage-error', '');
    text('updated', '');
    el('detail').setAttribute('aria-busy', 'false');
    text('detail-title', account?.name || 'Account');
    renderBadges(account);
    if (!account) { showDetail(false); return; }
    renderConnection(account);
    text('pause', account.enabled ? 'Pause' : 'Resume');
    el('quota-link').href = base + '/?provider=' + encodeURIComponent(account.provider) + '&account=' + encodeURIComponent(account.id);
    el('quota-link').hidden = account.mode === 'cost';
    // A phone list screen keeps its URL clean, so reloading it stays on the list.
    if (!narrow.matches || inDetail()) window.history.replaceState(window.history.state, '', accountURL(account.id));
    loadUsage();
  }
  async function loadAccounts(preferred = selected) {
    const data = await api('/api/accounts');
    accounts = Array.isArray(data.accounts) ? data.accounts : [];
    providers = Array.isArray(data.providers) ? data.providers : [];
    el('provider').replaceChildren();
    providers.forEach(provider => {
      const option = make('option', '', provider.label || provider.id);
      option.value = provider.id;
      el('provider').append(option);
    });
    const discovery = data.discovery || {};
    el('discover').dataset.available = String(!!(discovery.enabled && discovery.available));
    text('discovery-note', discovery.message || (discovery.enabled && discovery.available ? 'Optional: discover credentials from omp. Cost estimation does not require omp.' : 'Optional omp discovery is unavailable. Add accounts manually; costs do not require omp.'));
    setBusy(busy);
    selectAccount(accounts.find(a => String(a.id) === String(preferred))?.id ?? accounts[0]?.id ?? null);
  }
  function syncModeFields() {
    const definition = providers.find(p => p.id === el('provider').value);
    const quotaAllowed = !definition || definition.quota !== false;
    el('mode').querySelector('option[value="quota"]').disabled = !quotaAllowed;
    if (!quotaAllowed) el('mode').value = 'cost';
    el('billing-field').hidden = el('mode').value !== 'cost';
    text('mode-note', MODE_NOTES[el('mode').value] || '');
  }
  function credentialFields() {
    const account = editing;
    const readOnly = !!account && (account.readOnly || account.source === 'omp');
    const fields = providers.find(p => p.id === el('provider').value)?.fields || [];
    el('credential-fields').replaceChildren();
    text('credential-note', readOnly ? 'Discovered credentials are read-only and managed by omp.' : account ? 'Leave a field blank to keep its stored value. Stored values are never shown; API keys can be copied from the account page.' : 'Credentials are stored encrypted. API keys can be copied later from the account page.');
    fields.forEach((field, index) => {
      const label = make('label', '', field.label || field.key);
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
    syncModeFields();
  }
  function clearCredentials() { el('credential-fields').querySelectorAll('input').forEach(input => { input.value = ''; }); }
  function resetEditor() {
    clearCredentials();
    editing = null;
    el('form').reset();
    error('form-error', '');
  }
  function closeEditor() {
    if (el('editor').open) el('editor').close();
    resetEditor();
  }
  function openEditor(account) {
    closeEditor();
    editing = account || null;
    text('editor-title', account ? 'Edit ' + account.name : 'Add account');
    el('provider').disabled = !!account;
    el('provider').value = account?.provider || providers[0]?.id || '';
    // Legacy providers absent from the catalog still need editable account settings.
    if (account && !providers.some(p => p.id === account.provider)) {
      const option = make('option', '', providerLabel(account.provider));
      option.value = account.provider;
      el('provider').append(option);
      el('provider').value = account.provider;
    }
    el('name').value = account?.name || '';
    el('base-url').value = account?.baseUrl || '';
    el('mode').value = account?.mode || 'quota';
    el('billing').value = account?.billingKind || 'auto';
    el('enabled').checked = account ? !!account.enabled : true;
    credentialFields();
    el('editor').showModal();
    el(account ? 'name' : 'provider').focus();
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
  function emptyRow(body, columns) {
    const row = make('tr');
    const cell = make('td', 'accounts-table-empty', 'No observed usage for this period.');
    cell.colSpan = columns;
    row.append(cell);
    body.append(row);
  }
  function renderDaily(rows, currency) {
    const body = el('daily');
    body.replaceChildren();
    if (!Array.isArray(rows) || !rows.length) { emptyRow(body, 4); return; }
    const peak = Math.max(1, ...rows.map(row => isNumber(row.tokens) ? row.tokens : 0));
    [...rows].sort((a, b) => String(b.date).localeCompare(String(a.date))).forEach(item => {
      const row = make('tr');
      const tokens = make('td', 'accounts-bar-cell');
      const bar = make('span', 'accounts-bar');
      bar.setAttribute('aria-hidden', 'true');
      bar.style.width = Math.max(2, (isNumber(item.tokens) ? item.tokens : 0) / peak * 100) + '%';
      tokens.append(make('span', '', number(item.tokens)), bar);
      row.append(make('td', '', item.date ?? 'Unknown'), make('td', '', money(item.amount, currency)), tokens, make('td', '', number(item.requests)));
      body.append(row);
    });
  }
  function renderModels(rows, currency) {
    const body = el('models');
    body.replaceChildren();
    if (!Array.isArray(rows) || !rows.length) { emptyRow(body, 7); return; }
    const amount = row => isNumber(row.amount) ? row.amount : -1;
    [...rows].sort((a, b) => amount(b) - amount(a) || (b.requests || 0) - (a.requests || 0)).forEach(item => {
      const row = make('tr');
      row.append(make('td', 'accounts-model', item.model ?? 'Unknown'), make('td', '', money(item.amount, currency)));
      ['inputTokens', 'outputTokens', 'cacheReadTokens', 'cacheWriteTokens', 'requests'].forEach(key => row.append(make('td', '', number(item[key]))));
      body.append(row);
    });
  }
  // Quota responses differ per provider. Anything with a utilization percent
  // (or used/limit) becomes a meter and anything with total+currency a balance;
  // the raw response stays available for everything else.
  function quotaView(quota) {
    const meters = [], balances = [];
    const percentOf = item => {
      for (const key of ['utilization', 'percent', 'usagePercent', 'percentage']) if (isNumber(item[key])) return item[key];
      const used = isNumber(item.used) ? item.used : item.usage;
      return isNumber(used) && isNumber(item.limit) && item.limit > 0 ? used / item.limit * 100 : null;
    };
    const visit = (item, key) => {
      if (!item || typeof item !== 'object' || Array.isArray(item)) return;
      const name = item.displayName || item.name || key;
      const percent = percentOf(item);
      if (percent != null) {
        const used = isNumber(item.used) ? item.used : item.usage;
        const showAmounts = !item.percentOnly && item.format !== 'percent' && isNumber(used) && isNumber(item.limit) && item.limit > 0;
        meters.push({ name, percent: Math.max(0, percent), status: String(item.status || '').toLowerCase(), reset: item.timeUntilReset || '', amounts: showAmounts ? number(used) + ' / ' + number(item.limit) : '' });
      } else if (isNumber(item.total) && typeof item.currency === 'string') {
        balances.push({ name: item.name || key, total: item.total, currency: item.currency, granted: item.granted, toppedUp: item.toppedUp });
      }
    };
    Object.entries(quota).forEach(([key, value]) => (Array.isArray(value) ? value.forEach(entry => visit(entry, key)) : visit(value, key)));
    return { meters, balances, plan: quota.planLabel || quota.planName || quota.accountType || '' };
  }
  function tone(meter) {
    if (['critical', 'danger', 'exhausted', 'exceeded', 'limited'].includes(meter.status) || meter.percent >= 90) return 'is-bad';
    if (meter.status === 'warning' || meter.percent >= 70) return 'is-warn';
    return 'is-ok';
  }
  function renderQuota(data, account) {
    el('quota-panel').hidden = account.mode === 'cost';
    const container = el('quota-meters');
    container.replaceChildren();
    const quota = data.quota && typeof data.quota === 'object' ? data.quota : null;
    const hasQuota = !!quota && Object.keys(quota).length > 0;
    const view = hasQuota ? quotaView(quota) : { meters: [], balances: [], plan: '' };
    text('quota-plan', view.plan);
    el('quota-plan').hidden = !view.plan;
    view.balances.forEach(balance => {
      const card = make('div', 'accounts-meter accounts-balance');
      const head = make('div', 'accounts-meter-head');
      head.append(make('span', 'accounts-meter-name', balance.name), make('strong', 'accounts-meter-value', money(balance.total, balance.currency)));
      const parts = [isNumber(balance.toppedUp) ? 'Topped up ' + money(balance.toppedUp, balance.currency) : '', isNumber(balance.granted) ? 'Granted ' + money(balance.granted, balance.currency) : ''].filter(Boolean);
      card.append(head, make('div', 'accounts-meter-foot', parts.join(' · ')));
      container.append(card);
    });
    view.meters.forEach(meter => {
      const card = make('div', 'accounts-meter ' + tone(meter));
      const shown = Number(meter.percent.toFixed(meter.percent < 10 ? 1 : 0));
      const head = make('div', 'accounts-meter-head');
      head.append(make('span', 'accounts-meter-name', meter.name), make('strong', 'accounts-meter-value', shown + '%'));
      const track = make('div', 'accounts-meter-track');
      track.setAttribute('role', 'meter');
      track.setAttribute('aria-label', meter.name + ' used');
      track.setAttribute('aria-valuemin', '0');
      track.setAttribute('aria-valuemax', '100');
      track.setAttribute('aria-valuenow', String(Math.min(100, shown)));
      const fill = make('div', 'accounts-meter-fill');
      fill.style.width = Math.min(100, meter.percent) + '%';
      track.append(fill);
      const foot = make('div', 'accounts-meter-foot');
      foot.append(make('span', '', meter.amounts), make('span', '', meter.reset && meter.reset !== 'N/A' ? 'Resets in ' + meter.reset : ''));
      card.append(head, track, foot);
      container.append(card);
    });
    const recognized = view.meters.length + view.balances.length > 0;
    text('quota-empty', !hasQuota ? 'Quota data is not available yet. Open the quota dashboard for provider details.' : recognized ? '' : 'This provider reports quota in a custom format; see the raw data below.');
    el('quota-empty').hidden = hasQuota && recognized;
    text('quota-data', hasQuota ? JSON.stringify(quota, null, 2) : '');
    el('quota-raw').hidden = !hasQuota;
    el('quota-raw').open = hasQuota && !recognized;
  }
  function renderUsage(data, account) {
    const cost = data.cost || {};
    const currency = cost.currency || 'USD';
    const daily = Array.isArray(data.daily) ? data.daily : [];
    text('cost-amount', money(cost.amount, currency));
    el('cost-amount').classList.toggle('is-unknown', !isNumber(cost.amount));
    const source = { provider: 'Provider-reported', local_estimate: 'Local estimate', unavailable: 'No spend data' }[cost.source] || 'Unknown source';
    const period = cost.source === 'unavailable' ? '' : cost.periodStart === 'Lifetime' ? 'lifetime' : cost.source === 'provider' && cost.periodStart ? 'since ' + new Date(cost.periodStart).toLocaleDateString() : rangeLabel();
    text('cost-source', [source, period].filter(Boolean).join(' · '));
    const requests = daily.reduce((sum, row) => sum + (isNumber(row.requests) ? row.requests : 0), 0);
    text('total-requests', compact(requests));
    text('total-tokens', compact(daily.reduce((sum, row) => sum + (isNumber(row.tokens) ? row.tokens : 0), 0)));
    text('total-period', (requests ? 'observed, ' : 'no calls recorded, ') + rangeLabel());
    el('cost-facts').replaceChildren();
    [['Scope', cost.scope || 'Unknown; do not combine with other accounts'], ['Period start', cost.periodStart === 'Lifetime' ? 'Lifetime' : date(cost.periodStart)], ['Period end', date(cost.periodEnd)], ['Observed', date(cost.observedAt)], ['Status', cost.status || 'Unknown']].forEach(([label, value]) => {
      el('cost-facts').append(make('dt', '', label), make('dd', '', value));
    });
    text('usage-message', data.message || '');
    el('usage-message').hidden = !data.message;
    renderQuota(data, account);
    renderDaily(daily, currency);
    renderModels(data.models, currency);
    text('updated', 'Updated ' + new Date().toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }));
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
  async function copyAPIKey() {
    const account = current();
    if (!account?.hasApiKey) return;
    const button = el('copy-key');
    button.disabled = true;
    const pending = api('/api/accounts/key', { method: 'POST', body: JSON.stringify({ id: account.id }) }).then(data => {
      if (typeof data.apiKey !== 'string' || !data.apiKey) throw new Error('The server returned no API key.');
      return data.apiKey;
    });
    try {
      await writeClipboard(pending);
      flash(button, 'Copied');
      toast('API key for “' + account.name + '” copied to the clipboard.');
    } catch (err) {
      toast(err.message || 'Could not copy the API key.', true);
    } finally {
      button.disabled = false;
    }
  }
  el('add').addEventListener('click', () => openEditor(null));
  el('edit').addEventListener('click', () => { if (current()) openEditor(current()); });
  el('cancel').addEventListener('click', closeEditor);
  el('close').addEventListener('click', closeEditor);
  el('editor').addEventListener('close', resetEditor);
  el('editor').addEventListener('click', event => { if (event.target === el('editor')) closeEditor(); });
  el('provider').addEventListener('change', credentialFields);
  el('mode').addEventListener('change', syncModeFields);
  el('filter').addEventListener('input', renderList);
  el('copy-key').addEventListener('click', copyAPIKey);
  el('copy-url').addEventListener('click', async () => {
    const account = current();
    if (!account?.baseUrl) return;
    try { await writeClipboard(Promise.resolve(account.baseUrl)); flash(el('copy-url'), 'Copied'); }
    catch (err) { toast(err.message || 'Could not copy the base URL.', true); }
  });
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
      toast('Account saved.');
      await loadAccounts(id);
      revealDetail();
    } catch (err) { error(el('editor').open ? 'form-error' : 'error', err.message || 'Unable to save account.'); }
    finally { body.credentials = {}; clearCredentials(); setBusy(false); }
  });
  el('pause').addEventListener('click', () => mutate(async () => {
    const account = current();
    if (!account) return;
    const body = payload(account);
    body.enabled = !body.enabled;
    await api('/api/accounts', { method: 'PUT', body: JSON.stringify(body) });
    toast(body.enabled ? 'Account resumed.' : 'Account paused. Historical usage is retained.');
    await loadAccounts(account.id);
  }));
  el('delete').addEventListener('click', () => {
    const account = current();
    if (!account || !window.confirm('Delete “' + account.name + '”? Collection stops and history is retained.')) return;
    mutate(async () => {
      await api('/api/accounts?id=' + encodeURIComponent(account.id), { method: 'DELETE' });
      toast('Account deleted. Historical usage is retained.');
      // No history.back() here: it would race the URL update of the reload below.
      showDetail(false);
      await loadAccounts(null);
    });
  });
  el('discover').addEventListener('click', () => mutate(async () => {
    const data = await api('/api/accounts/discover', { method: 'POST' });
    toast((data.message || 'Discovery completed.') + (typeof data.imported === 'number' ? ' Imported: ' + data.imported + '.' : ''));
    await loadAccounts();
  }));
  el('range').addEventListener('change', () => { el('usage').hidden = true; loadUsage(); });
  el('refresh').addEventListener('click', loadUsage);
  document.addEventListener('keydown', event => {
    const target = event.target;
    const typing = target instanceof HTMLElement && (target.isContentEditable || ['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName));
    if (event.key === '/' && !typing && !el('editor').open && !event.ctrlKey && !event.metaKey && !event.altKey) {
      event.preventDefault();
      if (narrow.matches && inDetail()) backToList();
      el('filter').focus();
    }
  });
  el('back-list').addEventListener('click', backToList);
  window.addEventListener('popstate', () => { if (inDetail()) { showDetail(false); focusSelectedItem(); } });
  // The dashboard script owns this toggle elsewhere; it is not loaded here.
  document.getElementById('theme-toggle')?.addEventListener('click', () => {
    const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', next);
    try { localStorage.setItem('onwatch-theme', next); } catch (_) { /* Private mode: the choice lasts this page only. */ }
  });
  document.addEventListener('visibilitychange', () => { if (document.hidden) invalidateUsage(); else loadUsage(); });
  window.addEventListener('pagehide', () => { clearCredentials(); invalidateUsage(); });
  window.setInterval(() => { if (!document.hidden && !busy && !usageController) loadUsage(); }, 60000);
  const requested = new URLSearchParams(window.location.search).get('account');
  if (requested && narrow.matches) showDetail(true);
  loadAccounts(requested).catch(err => {
    error('error', err.message || 'Unable to load accounts.');
    text('list', 'Accounts could not be loaded. Reload the page to try again.');
  });
})();
