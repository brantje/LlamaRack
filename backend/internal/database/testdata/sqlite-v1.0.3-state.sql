-- Representative durable state for a database created by LlamaRack v1.0.3.
-- The test builds the schema from the exact released v1.0.3 migration set
-- (00001 + Go migration 00002 + 00003..00005) before applying this fixture.

INSERT INTO users(id,username,password_hash,enabled,created_at,last_login_at)
VALUES(42,'release-user','release-password-hash',1,1700000000,1700000100);

INSERT INTO sessions(id,user_id,token_hash,csrf_token_hash,created_at,expires_at,remote_address,user_agent)
VALUES('release-session',42,'release-session-hash','release-csrf-hash',1700000200,4102444800,'192.0.2.10','v1.0.3-fixture');

INSERT INTO service_accounts(id,name,enabled,created_at,created_by_user_id)
VALUES('release-service','Release Service',1,1700000300,42);

INSERT INTO api_keys(id,name,prefix,token_hash,key_type,owner_user_id,enabled,instance_ids,created_by_user_id,created_at,last_used_at)
VALUES(
  'release-key','Release Key','sk-release','release-key-hash','inference',42,1,
  '["11111111-1111-4111-8111-111111111111"]',42,1700000400,1700000500
);

INSERT INTO manager_settings(setting_key,setting_value,updated_at)
VALUES('release.setting','preserved',1700000600);

INSERT INTO provider_secrets(name,ciphertext,nonce,prefix,updated_at)
VALUES('huggingface',X'010203',X'040506','hf_rel',1700000700);

INSERT INTO models(id,slug,name,gguf_path,total_bytes,quantization,context_length,created_at,updated_at)
VALUES('release-model','release-model','Release Model','release/model.gguf',123456,'Q4_K_M',8192,1700000800,1700000800);

INSERT INTO model_options(model_id,option_key,option_value)
VALUES('release-model','ctx-size','8192');

INSERT INTO instances(
  id,slug,model_id,name,enabled,autoload_enabled,always_on,priority,eviction_enabled,
  idle_unload_seconds,max_pending_requests,gpu_mode,gpu_devices,tensor_split,request_log_mode,
  created_at,updated_at
) VALUES(
  '11111111-1111-4111-8111-111111111111','release-instance','release-model','Release Instance',
  1,1,1,'high',1,45,9,'manual','CUDA0','1','full',1700000900,1700000900
);

INSERT INTO instance_options(instance_id,option_key,option_value)
VALUES('11111111-1111-4111-8111-111111111111','flash-attn','true');

INSERT INTO download_jobs(
  id,provider,repo_id,revision,artifact_id,name,quantization,state,total_bytes,downloaded_bytes,
  speed_bps,error,created_at,updated_at
) VALUES(
  'release-download','huggingface','acme/release','revision-1','artifact-1','release.gguf','Q4_K_M',
  'COMPLETED',123456,123456,0,'',1700001000,1700001100
);

INSERT INTO download_files(job_id,path,size,oid,state,downloaded_bytes,etag,ordinal,local_path,temp_path)
VALUES(
  'release-download','release.gguf',123456,'oid-release','COMPLETED',123456,'etag-release',0,
  'huggingface/acme/release/release.gguf',''
);

INSERT INTO provider_imports(
  id,job_id,model_id,instance_id,owns_model,start_when_ready,state,error,start_attempted,created_at,updated_at
) VALUES(
  'release-import','release-download','release-model','11111111-1111-4111-8111-111111111111',
  1,1,'COMPLETED','',1,1700001200,1700001300
);

INSERT INTO worker_runtime(instance_id,generation,pid,start_ticks,port,updated_at)
VALUES('11111111-1111-4111-8111-111111111111','release-generation',1234,5678,10001,1700001400);

INSERT INTO inference_requests(
  id,started_at,finished_at,instance_id,model_slug,endpoint,api_key_id,api_key_name,api_key_prefix,
  streaming,status_code,result,duration_ms,ttft_ms,prompt_tokens,generated_tokens,total_tokens,
  tokens_per_second,queue_duration_ms,load_duration_ms,autoloaded,error,trace_id,call_type,client_ip,
  user_agent,owner_kind,owner_id
) VALUES(
  77,1700001500000,1700001500123,'11111111-1111-4111-8111-111111111111','release-instance',
  '/v1/chat/completions','release-key','Release Key','sk-release',0,200,'success',123,12,10,20,30,
  162.6,3,4,0,'','release-trace','chat','192.0.2.20','release-agent','api_key','release-key'
);

INSERT INTO inference_request_correlations(request_id,inference_request_id,prompt_tokens_per_second)
VALUES('release-request',77,83.3);

INSERT INTO inference_request_log_context(request_id,session_id,model_id,model_name)
VALUES('release-request','release-session','release-model','Release Model');

INSERT INTO inference_request_timings(
  request_id,prompt_n,prompt_ms,prompt_per_second,prompt_per_token_ms,predicted_n,predicted_ms,
  predicted_per_second,predicted_per_token_ms,cache_n,draft_n,draft_n_accepted,finish_reason,tool_call_count
) VALUES(
  'release-request',10,20,500,2,20,100,200,5,3,0,0,'stop',0
);

INSERT INTO observability_counters(metric,instance_id,endpoint,status_code,result,streaming,value)
VALUES('requests_total','11111111-1111-4111-8111-111111111111','/v1/chat/completions',200,'success',0,1);

INSERT INTO hardware_metric_samples(collected_at,metric,device_id,instance_id,value)
VALUES(1700001600000,'gpu_vram_used_bytes','CUDA0','11111111-1111-4111-8111-111111111111',1024);

INSERT INTO playground_lifecycle_events(event,instance_id,correlation_id)
VALUES('start','11111111-1111-4111-8111-111111111111','release-correlation');
