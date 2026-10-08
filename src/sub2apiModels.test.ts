import {expect,it,vi,afterEach} from 'vitest';
import {loadSubModels,saveSubModels,SUB_MODELS} from './sub2apiModels';
afterEach(()=>vi.unstubAllGlobals());
it('preserves extra-model choices across scopes, reloads and catalog additions',()=>{
 const user='scope-models',extra='private-custom';
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toContain(extra);
 saveSubModels(user,['gpt-6-astra'],[...SUB_MODELS,extra]);
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toEqual(['gpt-6-astra']);
 saveSubModels(user,['gpt-6-sol'],SUB_MODELS);
 expect(loadSubModels(user,[...SUB_MODELS,extra])).toEqual(['gpt-6-sol']);
 expect(loadSubModels(user,[...SUB_MODELS,extra,'new-extra'])).toEqual(['gpt-6-sol','new-extra']);
 expect(loadSubModels('other-user',[extra])).toEqual([extra]);
});
it('does not silently select all when every visible model has been disabled',()=>{
 saveSubModels('hidden-extra',['extra'],[...SUB_MODELS,'extra']);
 expect(loadSubModels('hidden-extra')).toEqual([]);
 expect(loadSubModels('hidden-extra',[...SUB_MODELS,'extra'])).toEqual(['extra']);
});
it('offers Haiku 5.5 in the shared channel model catalog without resetting saved exclusions',()=>{
 const user='haiku-catalog-upgrade';
 const previous=SUB_MODELS.filter(m=>m!=='claude-haiku-5-5');
 saveSubModels(user,previous.filter(m=>m!=='gpt-6-sol'),previous);
 expect(loadSubModels(user)).toContain('claude-haiku-5-5');
 expect(loadSubModels(user)).not.toContain('gpt-6-sol');
});
