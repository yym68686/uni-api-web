import {expect,it,vi,afterEach} from 'vitest';
import {initializeSubModels,loadSubModels,saveSubModels,SUB_MODELS,subModelProtocolLabel,subModelSelectionNeedsReview} from './sub2apiModels';
afterEach(()=>vi.unstubAllGlobals());
it('offers GPT 6.1 Terra with Responses without resetting previous exclusions',()=>{
 const model='gpt-6.1-terra',user='terra-61-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!==model);
 saveSubModels(user,previous.filter(m=>m!=='gpt-5.6-terra'),previous);
 expect(SUB_MODELS.filter(m=>m===model)).toHaveLength(1);
 expect(loadSubModels(user)).not.toContain(model);
 expect(loadSubModels(user)).not.toContain('gpt-5.6-terra');
 expect(subModelProtocolLabel(model)).toBe('Responses');
});
it('offers Kimi K3.1 Pro independently while preserving previous model exclusions',()=>{
 const model='kimi-k3.1-pro',user='kimi-31-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!==model);
 saveSubModels(user,previous.filter(m=>m!=='kimi-k3'),previous);
 expect(SUB_MODELS.filter(m=>m===model)).toHaveLength(1);
 expect(loadSubModels(user)).not.toContain(model);
 expect(loadSubModels(user)).not.toContain('kimi-k3');
 expect(subModelProtocolLabel(model)).toBe('Responses');
});
it('offers DeepSeek Flash 1017 as an independent Responses model while preserving saved exclusions',()=>{
 const model='deepseek-v4.1-flash-1017',user='deepseek-1017-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!==model);
 saveSubModels(user,previous.filter(m=>m!=='deepseek-v4.1-flash'),previous);
 expect(SUB_MODELS.filter(m=>m===model)).toHaveLength(1);
 expect(loadSubModels(user)).not.toContain(model);
 expect(loadSubModels(user)).not.toContain('deepseek-v4.1-flash');
 expect(subModelProtocolLabel(model)).toBe('Responses');
});
it('preserves extra-model choices across scopes, reloads and catalog additions',()=>{
 const user='scope-models',extra='private-custom';
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toEqual(SUB_MODELS);
 saveSubModels(user,['gpt-6-astra',extra],[...SUB_MODELS,extra]);
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toEqual(['gpt-6-astra',extra]);
 saveSubModels(user,['gpt-6-sol'],SUB_MODELS);
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toEqual(['gpt-6-sol',extra]);
 expect(loadSubModels(user,[...SUB_MODELS,extra,'new-extra'])).toEqual(['gpt-6-sol',extra]);
 expect(loadSubModels('other-user',[extra])).toEqual([]);
});
it('does not silently select all when every visible model has been disabled',()=>{
 saveSubModels('hidden-extra',['extra'],[...SUB_MODELS,'extra']);
 expect(loadSubModels('hidden-extra')).toEqual([]);
 expect(loadSubModels('hidden-extra',[...SUB_MODELS,'extra'])).toEqual(['extra']);
});

it('keeps a saved single-model scope when newly discovered extras enter the filtered catalog',()=>{
 const user='single-model-stable-scope';
 const oldCatalog=[...SUB_MODELS,'existing-extra'];
 saveSubModels(user,['gpt-6.1-terra'],oldCatalog);
 const expanded=[...oldCatalog,'new-extra-a','new-extra-b'];
 expect(loadSubModels(user,expanded)).toEqual(['gpt-6.1-terra']);
});

it('freezes the initial built-in default scope before future built-in or extra catalog additions',()=>{
 const user='initial-catalog-baseline',initial=[...SUB_MODELS,'existing-extra'];
 expect(loadSubModels(user,initial)).toEqual(SUB_MODELS);
 initializeSubModels(user,initial);
 expect(loadSubModels(user,[...initial,'new-extra','future-built-in'])).toEqual(SUB_MODELS);
});

it('preserves explicitly selected extras across filters while leaving unseen extras unselected',()=>{
 const user='explicit-extra-scope';
 const extra='selected-extra',newExtra='later-extra';
 saveSubModels(user,['gpt-6-sol',extra],[...SUB_MODELS,extra]);
 expect(loadSubModels(user,[...SUB_MODELS,newExtra])).toEqual(['gpt-6-sol']);
 expect(loadSubModels(user,[...SUB_MODELS,extra,newExtra])).toEqual(['gpt-6-sol',extra]);
});

it('safely migrates legacy exclusions and flags extra models for review',()=>{
 const user='legacy-model-selection';
 const disabled=SUB_MODELS.filter(model=>model!=='gpt-6.1-terra');
 localStorage.setItem(`uni-console-sub2api-models:v1:${user}`,JSON.stringify({disabled}));
 const extras=['previous-extra','new-extra'];
 expect(loadSubModels(user,[...SUB_MODELS,...extras])).toEqual(['gpt-6.1-terra']);
 expect(subModelSelectionNeedsReview(user)).toBe(true);
 saveSubModels(user,['gpt-6.1-terra','previous-extra'],[...SUB_MODELS,...extras]);
 expect(subModelSelectionNeedsReview(user)).toBe(false);
 expect(loadSubModels(user,[...SUB_MODELS,...extras,'later-extra'])).toEqual(['gpt-6.1-terra','previous-extra']);
});
it('does not expand saved scope when Haiku 5.5 enters the shared catalog',()=>{
 const user='haiku-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!=='claude-haiku-5-5');
 saveSubModels(user,previous.filter(m=>m!=='gpt-6-sol'),previous);
 expect(SUB_MODELS).toContain('claude-haiku-5-5');
 expect(loadSubModels(user)).not.toContain('claude-haiku-5-5');
 expect(loadSubModels(user)).not.toContain('gpt-6-sol');
});

it('does not expand saved scope when Gemini 4 Argon enters the shared catalog',()=>{
 const user='argon-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!=='gemini-4-argon');
 saveSubModels(user,previous.filter(m=>m!=='gpt-6-sol'),previous);
 expect(SUB_MODELS).toContain('gemini-4-argon');
 expect(loadSubModels(user)).not.toContain('gemini-4-argon');
 expect(loadSubModels(user)).not.toContain('gpt-6-sol');
 expect(subModelProtocolLabel('gemini-4-argon')).toBe('Gemini 原生');
});
