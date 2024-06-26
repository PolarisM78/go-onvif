/*
 * @Author: YanHui Li
 * @Date: 2022-01-04 16:17:53
 * @LastEditTime: 2022-02-24 16:55:34
 * @LastEditors: YanHui Li
 * @Description:
 * @FilePath: \go-onvif\Device.go
 *
 */
package onvif

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/PolarisM78/go-onvif/soap"
	"github.com/PolarisM78/go-onvif/types/device"

	"github.com/beevik/etree"
)

/* 定义设备参数结构体 */
type DeviceParams struct {
	Ipddr    string
	Username string
	Password string
	Uuid     string
	Types    string
	Name     string
	Model    string
	MAC      string
}

/* 定义设备控制句柄结构体 */
type Device struct {
	Params     DeviceParams
	httpClient *http.Client
	endpoints  map[string]string
}

// DeviceType alias for int
type DeviceType int

// Onvif Device Tyoe
const (
	NVD DeviceType = iota
	NVS
	NVA
	NVT
)

/* 定义DeviceType中String方法,用于索引转字符串信息 */
func (devType DeviceType) String() string {
	stringRepresentation := []string{
		"NetworkVideoDisplay",
		"NetworkVideoStorage",
		"NetworkVideoAnalytics",
		"NetworkVideoTransmitter",
	}
	i := uint8(devType)
	switch {
	case i <= uint8(NVT):
		return stringRepresentation[i]
	default:
		return strconv.Itoa(int(i))
	}
}

// Xlmns XML Scheam
var Xlmns = map[string]string{
	"onvif":   "http://www.onvif.org/ver10/schema",
	"tds":     "http://www.onvif.org/ver10/device/wsdl",
	"trt":     "http://www.onvif.org/ver10/media/wsdl",
	"tev":     "http://www.onvif.org/ver10/events/wsdl",
	"tptz":    "http://www.onvif.org/ver20/ptz/wsdl",
	"timg":    "http://www.onvif.org/ver20/imaging/wsdl",
	"tan":     "http://www.onvif.org/ver20/analytics/wsdl",
	"xmime":   "http://www.w3.org/2005/05/xmlmime",
	"wsnt":    "http://docs.oasis-open.org/wsn/b-2",
	"xop":     "http://www.w3.org/2004/08/xop/include",
	"wsa":     "http://www.w3.org/2005/08/addressing",
	"wstop":   "http://docs.oasis-open.org/wsn/t-1",
	"wsntw":   "http://docs.oasis-open.org/wsn/bw-2",
	"wsrf-rw": "http://docs.oasis-open.org/wsrf/rw-2",
	"wsaw":    "http://www.w3.org/2006/05/addressing/wsdl",
}

/* 初始化函数 */
func init() {
	/* 设置打印格式信息 */
	log.SetFlags(log.Lshortfile | log.LstdFlags)
}

/* 查找指定网卡支持onvif协议的NVT设备 */
func GetAvailableDevicesAtSpecificEthernetInterface(interfaceName string) []Device {
	/* Call an ws-discovery Probe Message to Discover NVT type Devices */
	devices := soap.SendProbe(interfaceName, nil, []string{"tds:" + NVT.String()}, map[string]string{"tds": "http://www.onvif.org/ver10/network/wsdl"})
	/* 遍历处理返回的设备数据 */
	nvtDevices := make([]Device, 0)
	for _, j := range devices {
		doc := etree.NewDocument()
		if err := doc.ReadFromString(j); err != nil {
			log.Printf("error:%s", err.Error())
			return nil
		}
		/* 查找ws-discovery中回复的设备地址信息 */
		endpoints := doc.Root().FindElements("./Body/ProbeMatches/ProbeMatch/XAddrs")
		for _, xaddr := range endpoints {
			xaddr := strings.Split(strings.Split(xaddr.Text(), " ")[0], "/")[2]
			c := 0
			for c = 0; c < len(nvtDevices); c++ {
				if nvtDevices[c].Params.Ipddr == xaddr {
					log.Printf(nvtDevices[c].Params.Ipddr, "==", xaddr)
					break
				}
			}
			if c < len(nvtDevices) {
				continue
			}
			/* 与设备建立连接获取服务地址信息 */
			dev, err := NewDevice(DeviceParams{Ipddr: strings.Split(xaddr, " ")[0]})
			if err != nil {
				log.Printf("error:%s", err.Error())
				continue
			} else {
				/* 获取uuid */
				endpoints = doc.Root().FindElements("./Body/ProbeMatches/ProbeMatch/EndpointReference/Address")
				dev.Params.Uuid = endpoints[0].Text()[strings.Index(endpoints[0].Text(), "uuid:")+5:]
				/* 获取设备基本信息 */
				endpoints = doc.Root().FindElements("./Body/ProbeMatches/ProbeMatch/Types")
				dev.Params.Types = endpoints[0].Text()
				endpoints = doc.Root().FindElements("./Body/ProbeMatches/ProbeMatch/Scopes")
				pointsString := strings.Split(endpoints[0].Text(), " ")
				for _, value := range pointsString {
					if strings.Contains(value, "MAC") {
						/* 获取设备mac */
						macString := strings.Split(value, "/")
						dev.Params.MAC = macString[len(macString)-1]
					} else if strings.Contains(value, "hardware") {
						/* 获取设备型号 */
						hardString := strings.Split(value, "/")
						dev.Params.Model = hardString[len(hardString)-1]
					} else if strings.Contains(value, "name") {
						/* 获取设备名称 */
						nameString := strings.Split(value, "/")
						dev.Params.Name = nameString[len(nameString)-1]
					}
				}
				nvtDevices = append(nvtDevices, *dev)
			}
		}
	}
	return nvtDevices
}

// NewDevice function construct a ONVIF Device entity
func NewDevice(params DeviceParams) (*Device, error) {
	dev := new(Device)
	dev.Params = params
	dev.endpoints = make(map[string]string)
	dev.addEndpoint("Device", "http://"+dev.Params.Ipddr+"/onvif/device_service")

	if dev.httpClient == nil {
		dev.httpClient = new(http.Client)
		/* 设置默认10s超时 */
		dev.httpClient.Timeout = time.Second * 10
	}
	/* 调用设备GetCapabilities方法获取能力合集 */
	getCapabilities := device.GetCapabilities{Category: "All"}
	resp, err := dev.CallMethod(getCapabilities)

	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, errors.New("camera is not available at " + dev.Params.Ipddr + " or it does not support ONVIF services")
	}
	/* 提前服务地址信息 */
	dev.getSupportedServices(resp)
	return dev, nil
}

func readResponse(resp *http.Response) []byte {
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	return b
}

// GetServices return available endpoints
func (dev *Device) GetServices() map[string]string {
	return dev.endpoints
}

func (dev *Device) getSupportedServices(resp *http.Response) {
	doc := etree.NewDocument()
	data, _ := ioutil.ReadAll(resp.Body)
	if err := doc.ReadFromBytes(data); err != nil {
		return
	}
	services := doc.FindElements("./Envelope/Body/GetCapabilitiesResponse/Capabilities/*/XAddr")
	for _, j := range services {
		dev.addEndpoint(j.Parent().Tag, j.Text())
	}
}

func (dev *Device) addEndpoint(Key, Value string) {
	//use lowCaseKey
	//make key having ability to handle Mixed Case for Different vendor devcie (e.g. Events EVENTS, events)
	lowCaseKey := strings.ToLower(Key)
	// Replace host with host from device params.
	if u, err := url.Parse(Value); err == nil {
		u.Host = dev.Params.Ipddr
		Value = u.String()
	}
	dev.endpoints[lowCaseKey] = Value
}

// getEndpoint functions get the target service endpoint in a better way
func (dev Device) getEndpoint(endpoint string) (string, error) {

	// common condition, endpointMark in map we use this.
	if endpointURL, bFound := dev.endpoints[endpoint]; bFound {
		return endpointURL, nil
	}

	//but ,if we have endpoint like event、analytic
	//and sametime the Targetkey like : events、analytics
	//we use fuzzy way to find the best match url
	var endpointURL string
	for targetKey := range dev.endpoints {
		if strings.Contains(targetKey, endpoint) {
			endpointURL = dev.endpoints[targetKey]
			return endpointURL, nil
		}
	}
	return endpointURL, errors.New("target endpoint service not found")
}

/**
 * @description:
 		CallMethod functions call an method, defined <method> struct.
		You should use Authenticate method to call authorized requests.
		Returns the corresponding struct
 * @param {interface{}} method call function struct
 * @param {interface{}} response return function response struct(take the address,Need to bring '&')
 * @param {string} RedirectURL event method uses a redirect URL
 * @return {error} error information
*/
//调用设备方法
func (dev Device) CallMethodInterface(method interface{}, response interface{}, RedirectURL string) error {
	/* 通过反射获取带入的结构体名称 */
	methodTypeName := reflect.TypeOf(method).String()
	responseTypeName := reflect.TypeOf(response).String()
	methodTypeName = methodTypeName[strings.Index(methodTypeName, ".")+1:]
	responseTypeName = responseTypeName[strings.Index(responseTypeName, ".")+1:]
	/* 判断调用的方法结构体是否和带入返回的结构体是一组 若不是则直接返回 */
	if fmt.Sprintf("%sResponse", methodTypeName) != responseTypeName {
		return errors.New("calls or returns struct parameter errors")
	}
	/* 获取调用方法的包名称 */
	pkgPath := strings.Split(reflect.TypeOf(method).PkgPath(), "/")
	pkg := strings.ToLower(pkgPath[len(pkgPath)-1])
	/* 获取调用方法的包对应的server地址 */
	endpoint, err := dev.getEndpoint(pkg)
	if err != nil {
		return err
	}
	if RedirectURL != "" {
		endpoint = RedirectURL
	}
	retResponse, err := dev.callMethodDo(endpoint, method)
	if err != nil {
		return err
	}
	/* 读取http返回数据 */
	retString := string(readResponse(retResponse))
	/* 定义处理解析的Body命名空间 */
	spaces := []string{"env", "s"}
	spacesIndex := -1
	/* 遍历查找设备Body使用的命名空间 */
	for index, value := range spaces {
		if strings.Index(retString, fmt.Sprintf("<%s:Body>", value)) > 0 && strings.Index(retString, fmt.Sprintf("</%s:Body>", value)) > 0 {
			spacesIndex = index
		}
	}
	/* 判断和提取选中的Body数据 */
	if spacesIndex >= 0 {
		startBodyLabel := fmt.Sprintf("<%s:Body>", spaces[spacesIndex])
		endBodyLabel := fmt.Sprintf("</%s:Body>", spaces[spacesIndex])
		bodyMsg := retString[strings.Index(retString, startBodyLabel)+len(startBodyLabel) : strings.Index(retString, endBodyLabel)]
		/* 检测设备是否发送fault信息 */
		if err := checkFaultCode(bodyMsg); err != nil {
			return err
		}
		/* 解析body中的xml信息 */
		if err := xml.Unmarshal([]byte(bodyMsg), &response); err != nil {
			return err
		} else {
			/* 成功返回 */
			return nil
		}
	}
	return errors.New("target returned an error")
}

// 检查错误状态码
func checkFaultCode(msg string) error {
	fault := device.FaultResponse{}
	xml.Unmarshal([]byte(msg), &fault)
	if fault.Reason.Text != "" {
		return errors.New(fault.Reason.Text)
	} else {
		return nil
	}
}

// CallMethod functions call an method, defined <method> struct.
// You should use Authenticate method to call authorized requests.
func (dev Device) CallMethod(method interface{}) (*http.Response, error) {
	pkgPath := strings.Split(reflect.TypeOf(method).PkgPath(), "/")
	pkg := strings.ToLower(pkgPath[len(pkgPath)-1])

	endpoint, err := dev.getEndpoint(pkg)
	if err != nil {
		return nil, err
	}
	return dev.callMethodDo(endpoint, method)
}

// CallMethod functions call an method, defined <method> struct with authentication data
func (dev Device) callMethodDo(endpoint string, method interface{}) (*http.Response, error) {
	output, err := xml.Marshal(method)
	if err != nil {
		return nil, err
	}
	soap, err := dev.buildMethodSOAP(string(output))
	if err != nil {
		return nil, err
	}
	soap.AddRootNamespaces(Xlmns)
	soap.AddAction()
	if dev.Params.Username != "" && dev.Params.Password != "" {
		soap.AddWSSecurity(dev.Params.Username, dev.Params.Password)
	}

	return SendSoap(dev.httpClient, endpoint, soap.String())
}

func (dev Device) buildMethodSOAP(msg string) (soap.SoapMessage, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(msg); err != nil {
		return "", err
	}
	element := doc.Root()
	soap := soap.NewEmptySOAP()
	soap.AddBodyContent(element)
	return soap, nil
}

// SendSoap send soap message
func SendSoap(httpClient *http.Client, endpoint, message string) (*http.Response, error) {
	resp, err := httpClient.Post(endpoint, "application/soap+xml; charset=utf-8", bytes.NewBufferString(message))
	if err != nil {
		return resp, err
	}
	return resp, nil
}
