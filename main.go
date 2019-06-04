package main

import (
	"fmt"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"log"
	"os"
	"time"
)

var (
	regionId        string
	accessKeyId     string
	accessKeySecret string
)

func main() {
	regionId = os.Getenv("REGION_ID")
	accessKeyId = os.Getenv("ACCESS_KEY_ID")
	accessKeySecret = os.Getenv("ACCESS_KEY_SECRET")

	if regionId == "" {
		log.Fatalln("环境变量 REGION_ID 不能为空！")
	}
	if accessKeyId == "" {
		log.Fatalln("环境变量 ACCESS_KEY_ID 不能为空！")
	}
	if accessKeySecret == "" {
		log.Fatalln("环境变量 ACCESS_KEY_SECRET 不能为空！")
	}

	ecsClient, err := ecs.NewClientWithAccessKey(regionId, accessKeyId, accessKeySecret)
	vpcClient, err := vpc.NewClientWithAccessKey(regionId, accessKeyId, accessKeySecret)

	// 张家口机房共享带宽ID
	bandwidthPackageId := "cbwp-8vbb2ec1dg7ew7quan51t"

	// 查询ecs主机的tag
	tags := &[]ecs.DescribeInstancesTag{
		{
			Value: "true",
			Key:   "rnode20",
		},
	}

	// AssociateEips 指定tag，给没有绑定eip的主机实例 绑定Eip
	AssociateEips(ecsClient, vpcClient, tags, bandwidthPackageId, err)
	//
	//// rnode起始ID
	//rnodeID := 9251
	//// 批量修改按tag查询的主机实例名
	//ModifyInstancesName(ecsClient, err, tags, rnodeID)

}

func GetInstanceEIPsByTags(client *ecs.Client, err error, tag *[]ecs.DescribeInstancesTag) []ecs.Instance {

	request := ecs.CreateDescribeInstancesRequest()

	request.Status = "running"
	request.PageNumber = "1"
	request.Tag = tag

	request.EipAddresses = ""

	response := GetInstancesByRequest(err, client, request)

	log.Printf("按tag查询到的实例总数: %v", response.TotalCount)

	pages := response.TotalCount / response.PageSize

	var instances []ecs.Instance

	for i := 1; i <= pages; i++ {
		request.PageNumber = requests.NewInteger(i)

		if i > 1 {
			response = GetInstancesByRequest(err, client, request)
		}

		for _, item := range response.Instances.Instance {
			if item.EipAddress.IpAddress != "" {
				instances = append(instances, item)
			}
		}
	}

	return instances

}

func GetInstanceIDsByNotEip(client *ecs.Client, tag *[]ecs.DescribeInstancesTag, err error) []ecs.Instance {

	request := ecs.CreateDescribeInstancesRequest()

	request.DryRun = "false"
	request.Status = "running"
	request.PageNumber = "1"
	request.Tag = tag

	response := GetInstancesByRequest(err, client, request)

	pages := response.TotalCount / response.PageSize

	log.Printf("按tag查询实例总数: %v", response.TotalCount)

	var instances []ecs.Instance

	for i := 1; i <= pages; i++ {
		request.PageNumber = requests.NewInteger(i)

		if i > 1 {
			response = GetInstancesByRequest(err, client, request)
		}

		for _, item := range response.Instances.Instance {
			if len(item.PublicIpAddress.IpAddress) == 0 && item.EipAddress.IpAddress == "" {
				instances = append(instances, item)
			}
		}
	}

	return instances

}

func GetInstancesByRequest(err error, client *ecs.Client, request *ecs.DescribeInstancesRequest) *ecs.DescribeInstancesResponse {
	response, err := client.DescribeInstances(request)
	if err != nil {
		fmt.Print(err.Error())
	}
	return response
}

//获取可用Eips
func GetAvailableEips(client *vpc.Client, err error) []vpc.EipAddress {
	request := vpc.CreateDescribeEipAddressesRequest()
	request.Status = "Available"
	response, err := client.DescribeEipAddresses(request)
	if err != nil {
		fmt.Print(err.Error())
	}
	return response.EipAddresses.EipAddress
}

//绑定eip
func AssociateEip(allocationId string, instanceId string, client *vpc.Client) error {
	request := vpc.CreateAssociateEipAddressRequest()

	request.AllocationId = allocationId
	request.InstanceId = instanceId

	i := 0
	for {
		response, err := client.AssociateEipAddress(request)
		if err != nil {
			if i < 4 {
				i++
				time.Sleep(1 * time.Second)
				continue
			}
			fmt.Printf("绑定eip失败: %s", response)
			return err
		}
		fmt.Printf("绑定eip成功，allocationId:  %s\n", allocationId)
		return nil
	}
}

// AssociateEips 指定tag，给没有绑定eip的主机实例 绑定Eip
func AssociateEips(ecsClient *ecs.Client, vpcClient *vpc.Client, tag *[]ecs.DescribeInstancesTag, bandwidthPackageId string, err error) {

	// 按tag获取没有eip的主机实例
	instances := GetInstanceIDsByNotEip(ecsClient, tag, err)
	log.Printf("按tag查询没有绑定Eip的实例数: %v", len(instances))
	// 获取空间Eips
	eipAddresses := GetAvailableEips(vpcClient, err)

	// 空闲eip不足的数量
	AddEipCounts := len(instances) - len(eipAddresses)

	log.Printf("新申请Eip数量: %v", AddEipCounts)

	if AddEipCounts > 0 {
		// 空闲eip不足的数量，重新申请eip 并加入到共享带宽
		AddEipToCommonBandwidth(AddEipCounts, bandwidthPackageId, err, vpcClient)
	} else {
		// 释放共享带宽中的空闲Eip
		ReleaseEipFromCommonBandwidth(vpcClient, err, bandwidthPackageId)
	}

	for num, instance := range instances {
		fmt.Sprintf("绑定 Eip: %s, InstanceId: %s", eipAddresses[num].IpAddress, instance)

		AssociateEip(eipAddresses[num].AllocationId, fmt.Sprint(instance.EipAddress), vpcClient)
		num++
	}
}

// 修改实例名称
func ModifyInstanceName(ecsClient *ecs.Client, instanceID string, instanceName string, err error) {
	request := ecs.CreateModifyInstanceAttributeRequest()

	request.InstanceId = instanceID
	request.InstanceName = instanceName

	response, err := ecsClient.ModifyInstanceAttribute(request)
	if err != nil {
		fmt.Print(err.Error())
	}

	if !response.IsSuccess() {
		fmt.Printf("modify error by instanceID: %s ,  instanceName: %s ", instanceID, instanceName)
	}
}

func ModifyInstancesName(ecsClient *ecs.Client, err error, tags *[]ecs.DescribeInstancesTag, rnodeID int) {
	// 指定tag获取实例
	instances := GetInstanceEIPsByTags(ecsClient, err, tags)
	for _, instance := range instances {
		fmt.Printf("instanceid %s , hostname: %s , pip: %s , eip: %s\n", instance.InstanceId, fmt.Sprintf("rnode%d.hisun.com", rnodeID), instance.VpcAttributes.PrivateIpAddress.IpAddress[0], instance.EipAddress.IpAddress)
		ModifyInstanceName(ecsClient, instance.InstanceId, fmt.Sprintf("rnode%d", rnodeID), err)
		rnodeID++
	}
}

func ReleaseEipFromCommonBandwidth(vpcClient *vpc.Client, err error, bandwidthPackageId string) {
	eipAddresses := GetAvailableEips(vpcClient, err)
	for _, eipAddress := range eipAddresses {
		log.Printf("释放Eip: %s\n", eipAddress.IpAddress)
		// eip 移动共享带宽
		RemoveCommonBandwidthPackageIp(eipAddress.AllocationId, bandwidthPackageId, err, vpcClient)
		// 释放Eip
		ReleaseEip(eipAddress.AllocationId, err, vpcClient)
	}
}

func AddEipToCommonBandwidth(eipCounts int, bandwidthPackageId string, err error, vpcClient *vpc.Client) {
	for i := 0; i < eipCounts; i++ {
		// 申请Eip,返回eip实例id
		ipInstanceID := AllocateEip(err, vpcClient)
		// 张家口机房共享带宽ID
		bandwidthPackageId := bandwidthPackageId
		// eip添加到共享带宽
		AddCommonBandwidthPackageIp(ipInstanceID, bandwidthPackageId, err, vpcClient)
	}
}

func ReleaseEip(ipInstanceID string, err error, vpcClient *vpc.Client) {
	request := vpc.CreateReleaseEipAddressRequest()
	request.AllocationId = ipInstanceID
	response, err := vpcClient.ReleaseEipAddress(request)
	if err != nil {
		fmt.Print(err.Error())
	}

	if !response.IsSuccess() {
		fmt.Printf("释放eip错误，response is %#v\n", response)
	}
}

func RemoveCommonBandwidthPackageIp(ipInstanceID string, bandwidthPackageId string, err error, vpcClient *vpc.Client) {
	request := vpc.CreateRemoveCommonBandwidthPackageIpRequest()
	request.IpInstanceId = ipInstanceID
	request.BandwidthPackageId = bandwidthPackageId
	response, err := vpcClient.RemoveCommonBandwidthPackageIp(request)
	if err != nil {
		fmt.Print(err.Error())
	}

	if !response.IsSuccess() {
		fmt.Printf("Eip移出共享带宽错误，response is %#v\n", response)
	}
}

func AddCommonBandwidthPackageIp(ipInstanceID string, bandwidthPackageId string, err error, vpcClient *vpc.Client) {
	request := vpc.CreateAddCommonBandwidthPackageIpRequest()
	request.IpInstanceId = ipInstanceID
	request.BandwidthPackageId = bandwidthPackageId
	response, err := vpcClient.AddCommonBandwidthPackageIp(request)
	if err != nil {
		fmt.Print(err.Error())
	}

	if !response.IsSuccess() {
		fmt.Printf("eip添加到共享带宽错误，response is %#v\n", response)
	}
}

func AllocateEip(err error, vpcClient *vpc.Client) string {
	request := vpc.CreateAllocateEipAddressRequest()
	request.ISP = "BGP"
	request.InternetChargeType = "PayByTraffic"
	response, err := vpcClient.AllocateEipAddress(request)
	if err != nil {
		fmt.Print(err.Error())
	}
	return response.AllocationId
}
