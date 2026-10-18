// CPU-only fake NVIDIA ABI. It never opens a GPU or calls a real driver.
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static long setting(const char *key, long fallback) { const char *s=getenv(key); return s ? strtol(s,0,10) : fallback; }
static void note(const char *call) { const char *p=getenv("GDS_FAKE_LOG"); if(p){FILE *f=fopen(p,"a");if(f){fprintf(f,"%s\n",call);fclose(f);}} }
static const char *UUID="GPU-00112233-4455-6677-8899-aabbccddeeff";
typedef struct { unsigned char bytes[16]; } CUuuid;
#ifndef PROCESS_VERSION
#define PROCESS_VERSION 3
#endif
#if PROCESS_VERSION == 1
typedef struct { unsigned int pid; unsigned long long used; } process_info;
#else
typedef struct { unsigned int pid; unsigned long long used; unsigned int gpu,compute; } process_info;
#endif
typedef struct { unsigned long long total,free,used; } memory_info;
typedef struct { unsigned int gpu,memory; } utilization_info;
static size_t reserved;

int nvmlInit_v2(void){note("nvml-init");return setting("GDS_FAKE_NVML_INIT_RET",0);}
int nvmlInit(void){return nvmlInit_v2();}
int nvmlShutdown(void){note("nvml-shutdown");return setting("GDS_FAKE_SHUTDOWN_RET",0);}
const char *nvmlErrorString(int code){return "fake NVML error";}
int nvmlDeviceGetHandleByUUID(const char *uuid,void **d){if(strcmp(uuid,UUID))return 6;*d=(void*)1;return 0;}
int nvmlDeviceGetUUID(void *d,char *out,unsigned int n){snprintf(out,n,"%s",setting("GDS_FAKE_BAD_UUID",0)?"GPU-ffffffff-ffff-ffff-ffff-ffffffffffff":UUID);return 0;}
int nvmlDeviceGetMigMode(void *d,unsigned int *current,unsigned int *pending){*current=setting("GDS_FAKE_MIG",0);*pending=setting("GDS_FAKE_MIG_PENDING",0);return setting("GDS_FAKE_MIG_RET",0);}
int nvmlDeviceGetVirtualizationMode(void *d,unsigned int *v){*v=setting("GDS_FAKE_VIRT",0);return setting("GDS_FAKE_VIRT_RET",0);}
int nvmlDeviceGetComputeMode(void *d,unsigned int *v){*v=setting("GDS_FAKE_MODE",0);return setting("GDS_FAKE_MODE_RET",0);}
int nvmlDeviceGetMemoryInfo(void *d,memory_info *m){m->total=16ULL<<30;m->free=12ULL<<30;m->used=4ULL<<30;return setting("GDS_FAKE_MEMORY_RET",0);}
int nvmlDeviceGetUtilizationRates(void *d,utilization_info *u){u->gpu=setting("GDS_FAKE_UTIL",0);u->memory=0;return setting("GDS_FAKE_UTIL_RET",0);}
static int processes(unsigned int *n,process_info *p,const char *key,const char *ret){
    note("process-query");
    int r=setting(ret,0);if(r)return r;
    if(setting("GDS_FAKE_CHURN",0)){*n+=1;return 7;}
    long count_value=setting(key,reserved&&!strcmp(key,"GDS_FAKE_COMPUTE_COUNT")?1:0);
    if(reserved){char after[96];snprintf(after,sizeof(after),"GDS_FAKE_AFTER_%s",key+strlen("GDS_FAKE_"));count_value=setting(after,count_value);}
    unsigned int count=count_value;if(*n<count){*n=count;return 7;}
    for(unsigned int i=0;i<count;i++){
        p[i].pid=setting("GDS_FAKE_PROCESS_PID",getpid());
        if(reserved)p[i].pid=setting("GDS_FAKE_AFTER_PROCESS_PID",p[i].pid);
        p[i].used=1024;
#if PROCESS_VERSION >= 2
        p[i].gpu=setting("GDS_FAKE_PROCESS_GPU_INSTANCE",0xffffffff);
        p[i].compute=setting("GDS_FAKE_PROCESS_COMPUTE_INSTANCE",0xffffffff);
#endif
    }
    *n=count;return 0;
}
#if PROCESS_VERSION == 3
int nvmlDeviceGetComputeRunningProcesses_v3(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_COMPUTE_COUNT","GDS_FAKE_COMPUTE_RET");}
int nvmlDeviceGetGraphicsRunningProcesses_v3(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_GRAPHICS_COUNT","GDS_FAKE_GRAPHICS_RET");}
int nvmlDeviceGetMPSComputeRunningProcesses_v3(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_MPS_COUNT","GDS_FAKE_MPS_RET");}
#elif PROCESS_VERSION == 2
int nvmlDeviceGetComputeRunningProcesses_v2(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_COMPUTE_COUNT","GDS_FAKE_COMPUTE_RET");}
int nvmlDeviceGetGraphicsRunningProcesses_v2(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_GRAPHICS_COUNT","GDS_FAKE_GRAPHICS_RET");}
int nvmlDeviceGetMPSComputeRunningProcesses_v2(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_MPS_COUNT","GDS_FAKE_MPS_RET");}
#else
int nvmlDeviceGetComputeRunningProcesses(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_COMPUTE_COUNT","GDS_FAKE_COMPUTE_RET");}
int nvmlDeviceGetGraphicsRunningProcesses(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_GRAPHICS_COUNT","GDS_FAKE_GRAPHICS_RET");}
int nvmlDeviceGetMPSComputeRunningProcesses(void *d,unsigned int *n,process_info *p){return processes(n,p,"GDS_FAKE_MPS_COUNT","GDS_FAKE_MPS_RET");}
#endif

int cuInit(unsigned int flags){note("cu-init");return setting("GDS_FAKE_CUDA_INIT_RET",0);}
static int selected(int ordinal){const char *v=getenv("CUDA_VISIBLE_DEVICES");if(!v)return ordinal;if(!strcmp(v,"1,0"))return ordinal?0:1;if(!strcmp(v,UUID))return 0;if(!strcmp(v,"0"))return 0;if(!strcmp(v,"1"))return 1;return ordinal;}
int cuDeviceGetCount(int *n){const char *v=getenv("CUDA_VISIBLE_DEVICES");*n=!v?2:!strcmp(v,"")?0:(!strcmp(v,"1,0")?2:1);return 0;}
int cuDeviceGet(int *device,int ordinal){*device=selected(ordinal);return 0;}
int cuDeviceGetUuid(CUuuid *uuid,int device){for(int i=0;i<16;i++)uuid->bytes[i]=device?0xff:(unsigned char)(i*0x11);return 0;}
int cuDeviceGetUuid_v2(CUuuid *uuid,int device){return cuDeviceGetUuid(uuid,device);}
int cuDeviceGetAttribute(int *value,int attribute,int device){*value=setting("GDS_FAKE_CUDA_MODE",0);return 0;}
int cuCtxCreate_v2(void **ctx,unsigned int flags,int device){note("ctx-create");*ctx=(void*)1;return setting("GDS_FAKE_CONTEXT_RET",0);}
int cuCtxDestroy_v2(void *ctx){note("ctx-destroy");return setting("GDS_FAKE_DESTROY_RET",0);}
int cuMemGetInfo_v2(size_t *free,size_t *total){*total=16ULL<<30;*free=reserved?(size_t)setting("GDS_FAKE_AFTER_FREE_BYTES",(12ULL<<30)-reserved):(size_t)setting("GDS_FAKE_FREE_BYTES",12ULL<<30);return setting("GDS_FAKE_INFO_RET",0);}
#ifndef OMIT_CUDA_ALLOC
int cuMemAlloc_v2(unsigned long long *ptr,size_t bytes){note("mem-allocate");int r=setting("GDS_FAKE_ALLOC_RET",0);if(!r){*ptr=1024;reserved=bytes;}return r;}
#endif
int cuMemFree_v2(unsigned long long ptr){note("mem-free");reserved=0;return setting("GDS_FAKE_FREE_RET",0);}
