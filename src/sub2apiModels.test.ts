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
