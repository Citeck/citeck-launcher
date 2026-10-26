#!/usr/bin/env node
// Run from the repository with an explicitly disposable platform and safe SSH helper.
// Credentials stay in memory; response bodies, cookies and raw configuration are not logged.
import {createRequire} from 'node:module';
import {execFileSync} from 'node:child_process';
import {mkdirSync, readFileSync} from 'node:fs';
import {createHash, randomUUID} from 'node:crypto';
import {resolve} from 'node:path';
const require = createRequire(new URL('../../web/package.json', import.meta.url));
const {chromium} = require('@playwright/test');
const [helperArg, engineID, origin, outputArg] = process.argv.slice(2);
if (!helperArg || !engineID || !origin || !outputArg) throw new Error('Expected SSH helper, engine ID, HTTPS origin and output directory');
if (process.argv.includes('--verify-file-card') && !process.argv.includes('--exercise-disposable-server')) throw new Error('File-card check requires disposable-server mutations');
const helper = resolve(helperArg), output = resolve(outputArg);
if (new URL(origin).protocol !== 'https:') throw new Error('HTTPS origin required');
const remote = command => execFileSync(helper, [command], {encoding:'utf8', timeout:30000});
if (remote("docker info --format '{{.ID}}'").trim() !== engineID) throw new Error('Wrong Docker engine');
mkdirSync(output, {recursive:true});
let password = remote(`python3 -c 'import json;print(json.load(open("/opt/citeck/conf/kv-state.json"))["_sys_admin_password"],end="")'`);
const browser = await chromium.launch({headless:true, executablePath:process.env.CITECK_TEST_CHROME || '/usr/bin/google-chrome'});
let stage = 'login';
try {
  const context = await browser.newContext({ignoreHTTPSErrors:true, viewport:{width:1440,height:1000}});
  const page = await context.newPage();
  let failures = [];
  page.on('response', r => {if (r.status() >= 500) failures.push({path:new URL(r.url()).pathname,status:r.status()});});
  await page.goto(origin, {waitUntil:'domcontentloaded',timeout:60000});
  await page.locator('#username').fill('admin');
  await page.locator('#password').fill(password); password = '';
  await page.locator('#kc-login').click();
  await page.waitForURL(u => u.pathname.startsWith('/v2/'), {timeout:120000});
  stage = 'platform-initialization';
  const deadline = Date.now() + 300000;
  let transientFailures = 0;
  while (true) {
    await page.waitForTimeout(5000);
    const ready = await page.getByText('Available spaces', {exact:true}).isVisible();
    // The heading renders before its widgets finish: a screenshot of their
    // spinners is not proof that the dashboard initialized successfully.
    const loading = await page.locator('[class*="loader"]:visible,[class*="spinner"]:visible').count();
    if (ready && loading === 0 && failures.length === 0) break;
    transientFailures += failures.length; failures = [];
    if (Date.now() >= deadline) throw new Error('Platform initialization timeout');
    await page.reload({waitUntil:'domcontentloaded',timeout:30000});
  }
  await page.screenshot({path:output+'/dashboard.png',fullPage:true,timeout:30000});
  console.log(JSON.stringify({check:'admin-dashboard',passed:true,transientFailures}));
  stage = 'documents-library'; failures = [];
  await page.getByText('Documents library', {exact:true}).click();
  await page.getByText('Workspace files', {exact:true}).first().waitFor({timeout:60000});
  await page.waitForTimeout(3000);
  if (failures.length) throw new Error('Library received server errors');
  await page.screenshot({path:output+'/documents-library.png',fullPage:true,timeout:30000});
  console.log(JSON.stringify({check:'documents-library',passed:true}));
  if (process.argv.includes('--exercise-disposable-server')) {
    stage = 'file-create'; failures = [];
    const stem = 'launcher-acceptance-' + randomUUID(), name = stem + '.txt';
    const expected = Buffer.alloc(1024 * 1024, 65);
    const uploaded = page.waitForResponse(r => new URL(r.url()).pathname.endsWith('/api/ecos/webapp/content') && r.request().method() === 'POST', {timeout:60000});
    void uploaded.catch(() => {});
    await page.locator('button[title="Create"]').click();
    await page.getByText('File', {exact:true}).click();
    await page.locator('input[type=file]').setInputFiles({name,mimeType:'text/plain',buffer:expected});
    const uploadResponse = await uploaded;
    if (uploadResponse.status() !== 200) throw new Error('Content upload failed');
    await uploadResponse.finished();
    await page.waitForTimeout(500); // Let the file component consume the completed upload.
    await page.locator('input[name="data[name]"]').fill(stem);
    await page.getByRole('button', {name:'Save',exact:true}).click();
    await page.getByText('Create new "File"', {exact:true}).waitFor({state:'hidden',timeout:60000});
    const file = page.locator('.ecos-files-viewer__item-title').filter({hasText:name});
    await file.waitFor({timeout:60000}); await file.hover();
    stage = 'file-download';
    const row = file.locator('xpath=ancestor::*[.//button[@title="Download"]][1]');
    const [download] = await Promise.all([
      page.waitForEvent('download', {timeout:60000}),
      row.locator('button[title="Download"]').click()
    ]);
    const actual = readFileSync(await download.path());
    if (!actual.equals(expected)) throw new Error('Downloaded content mismatch');
    console.log(JSON.stringify({check:'file-create-download',passed:true,name,bytes:actual.length,sha256:createHash('sha256').update(actual).digest('hex')}));
    const libraryUrl = page.url();
    let cardFailures = [], cardError = false;
    if (process.argv.includes('--verify-file-card')) {
      stage = 'file-card'; failures = [];
      try {
        await file.dblclick();
        await page.getByText('Preview', {exact:true}).first().waitFor({timeout:60000});
        await page.waitForTimeout(5000);
        cardFailures = [...failures];
        await page.screenshot({path:output+'/file-card.png',fullPage:true,timeout:30000});
      } catch {cardError = true;}
      await page.goto(libraryUrl, {waitUntil:'domcontentloaded',timeout:60000});
      await file.waitFor({timeout:60000});
      cardFailures.push(...failures.filter(f => !cardFailures.includes(f))); failures = [];
    }
    stage = 'file-delete-open'; await file.hover();
    await row.locator('button[title="Delete"]').click();
    stage = 'file-delete-confirm';
    await page.locator('button:visible').filter({hasText:/^Delete$/}).click();
    stage = 'file-delete-observe';
    await file.waitFor({state:'hidden',timeout:60000});
    console.log(JSON.stringify({check:'owned-file-delete',passed:true}));
    if (process.argv.includes('--verify-file-card')) cardFailures.push(...failures);
    else if (failures.length) throw new Error('File lifecycle received server errors');
    await page.screenshot({path:output+'/after-file-delete.png',fullPage:true,timeout:30000});
    if (cardError || cardFailures.length) {
      stage = 'file-card';
      console.log(JSON.stringify({check:'file-card',passed:false,serverFailures:cardFailures,cardError}));
      throw new Error('File card received server errors');
    }
    if (process.argv.includes('--verify-file-card')) console.log(JSON.stringify({check:'file-card',passed:true}));
  }
  await context.close();
} catch (error) {
  console.log(JSON.stringify({passed:false,stage,errorType:error.name})); process.exitCode=1;
} finally {password=''; await browser.close();}
