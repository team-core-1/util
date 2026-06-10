SHELL = /bin/bash

# 탭(Tab) 대신 '>' 문자를 사용하여 명령어를 구분하도록 설정
.RECIPEPREFIX = >

.PHONY: api pkg clean

# 고정 환경 변수
OAPI_CODEGEN_PATH = ./bin/oapi-codegen
OUTPUT_DIR = ./output
PKG_DIR = ./pkg/openapi/

# openAPI 추가할 때마다 직접 수정
SPEC_DIR = ./spec/rel16_202206
TS29571_CommonData = CommonData
TS29509_Nausf_SoRProtection = SoRProtection
TS29509_Nausf_UPUProtection = UPUProtection
TS29572_Nlmf_Location = Location
TS29503_Nudm_PP = PP
TS29507_Npcf_AMPolicyControl = AMPolicyControl
TS29512_Npcf_SMPolicyControl = SMPolicyControl
TS29525_Npcf_UEPolicyControl = UEPolicyControl
TS29518_Namf_Communication = Communication
TS29502_Nsmf_PDUSession = PDUSession
TS29503_Nudm_SDM = SDM
TS29503_Nudm_UECM = UECM

api:
> @echo "Generating OpenAPI models..."

> $(OAPI_CODEGEN_PATH) -config ./config/config.yaml \
	-o $(OUTPUT_DIR)/$(TS29571_CommonData).go \
	$(SPEC_DIR)/TS29571_CommonData.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29509_Nausf_SoRProtection) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29509_Nausf_SoRProtection).go \
	$(SPEC_DIR)/TS29509_Nausf_SoRProtection.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29509_Nausf_UPUProtection) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData),TS29509_Nausf_SoRProtection.yaml:$(TS29509_Nausf_SoRProtection) \
	-o $(OUTPUT_DIR)/$(TS29509_Nausf_UPUProtection).go \
	$(SPEC_DIR)/TS29509_Nausf_UPUProtection.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29572_Nlmf_Location) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29572_Nlmf_Location).go \
	$(SPEC_DIR)/TS29572_Nlmf_Location.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29503_Nudm_PP) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData),TS29572_Nlmf_Location.yaml:$(TS29572_Nlmf_Location),TS29509_Nausf_SoRProtection.yaml:$(TS29509_Nausf_SoRProtection) \
	-o $(OUTPUT_DIR)/$(TS29503_Nudm_PP).go \
	$(SPEC_DIR)/TS29503_Nudm_PP.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29507_Npcf_AMPolicyControl) \
	-generate models,gin \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29507_Npcf_AMPolicyControl).go \
	$(SPEC_DIR)/TS29507_Npcf_AMPolicyControl.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29512_Npcf_SMPolicyControl) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29512_Npcf_SMPolicyControl).go \
	$(SPEC_DIR)/TS29512_Npcf_SMPolicyControl.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29525_Npcf_UEPolicyControl) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29525_Npcf_UEPolicyControl).go \
	$(SPEC_DIR)/TS29525_Npcf_UEPolicyControl.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29518_Namf_Communication) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29518_Namf_Communication).go \
	$(SPEC_DIR)/TS29518_Namf_Communication.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29502_Nsmf_PDUSession) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData) \
	-o $(OUTPUT_DIR)/$(TS29502_Nsmf_PDUSession).go \
	$(SPEC_DIR)/TS29502_Nsmf_PDUSession.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29503_Nudm_SDM) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData),TS29503_Nudm_PP.yaml:$(TS29503_Nudm_PP),TS29509_Nausf_SoRProtection.yaml:$(TS29509_Nausf_SoRProtection),TS29509_Nausf_UPUProtection.yaml:$(TS29509_Nausf_UPUProtection),TS29572_Nlmf_Location.yaml:$(TS29572_Nlmf_Location) \
	-o $(OUTPUT_DIR)/$(TS29503_Nudm_SDM).go \
	$(SPEC_DIR)/TS29503_Nudm_SDM.yaml

> $(OAPI_CODEGEN_PATH) -package $(TS29503_Nudm_UECM) \
	-generate models \
	-import-mapping=TS29571_CommonData.yaml:$(TS29571_CommonData),TS29503_Nudm_SDM.yaml:$(TS29503_Nudm_SDM) \
	-o $(OUTPUT_DIR)/$(TS29503_Nudm_UECM).go \
	$(SPEC_DIR)/TS29503_Nudm_UECM.yaml

> @echo "Generation complete"

pkg: api
> @echo "Copy models.go files to $(PKG_DIR)"
> cp $(OUTPUT_DIR)/*go $(PKG_DIR)
# import path를 변경하는 부분 추가

clean:
> rm -f $(OUTPUT_DIR)/*.go
> rm -f $(PKG_DIR)/*.go
